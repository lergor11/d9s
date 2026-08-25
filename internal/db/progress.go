package db

import (
	"context"
	"sync"
)

// Progress is the accumulated read state of one running statement, as the
// engine reports it.
type Progress struct {
	// Rows and Bytes are how much the engine has read so far.
	Rows, Bytes uint64
	// TotalRows is how many rows the engine expects to read, 0 when it has
	// not said. With it, Rows over TotalRows is the fraction complete.
	TotalRows uint64
	// Memory is how many bytes of memory the statement holds on the server,
	// 0 when unreported. It comes from the MemoryTracking profile event.
	Memory int64
}

// ProfileEvent is one named counter the engine reported for a statement,
// such as SelectedRows or NetworkSendBytes.
type ProfileEvent struct {
	// Name is the engine's own name for the counter.
	Name string
	// Value is the counter's accumulated value.
	Value int64
}

// LogLine is one log line the server sent alongside a statement.
type LogLine struct {
	// Level is the server's severity for the line, e.g. "information".
	Level string
	// Source is the server component that wrote the line.
	Source string
	// Text is the line itself.
	Text string
}

// ProgressSink receives what an engine reports while a statement runs. Calls
// arrive on the goroutine reading the result — during Query and NextPage —
// and must return quickly without blocking. Progress and ProfileEvents each
// supersede their previous call; Log lines accumulate.
type ProgressSink interface {
	// Progress delivers the running totals so far.
	Progress(Progress)
	// ProfileEvents delivers the counters accumulated so far, in the order
	// each was first seen.
	ProfileEvents([]ProfileEvent)
	// Log delivers one server log line as it arrives.
	Log(LogLine)
}

// ProgressStreamer is implemented by drivers that can report engine progress
// while a statement runs. It is kept out of Driver the way Streamer is, so
// an engine without a progress stream is unaffected.
type ProgressStreamer interface {
	// ExecuteStreamProgress is ExecuteStream with the engine's progress,
	// profile and log reports delivered to sink until the result is drained.
	ExecuteStreamProgress(ctx context.Context, statement string, sink ProgressSink) (Cursor, error)
}

// StreamProgress is Stream with sink receiving engine progress where the
// driver can provide it. Any other driver runs plain Stream and the sink
// hears nothing, which the caller should render as elapsed time alone rather
// than inventing figures.
func StreamProgress(ctx context.Context, d Driver, statement string, sink ProgressSink) (Cursor, error) {
	if p, ok := d.(ProgressStreamer); ok && sink != nil {
		return p.ExecuteStreamProgress(ctx, statement, sink)
	}
	return Stream(ctx, d, statement)
}

// memoryTrackingEvent is the gauge ClickHouse reports a query's memory under.
const memoryTrackingEvent = "MemoryTracking"

// progressTracker folds an engine's per-packet reports into running totals
// for a sink. ClickHouse sends row and byte counts as increments per packet
// and profile events as per-thread deltas or gauges; the tracker sums what
// accumulates and keeps the last value of what does not.
type progressTracker struct {
	mu    sync.Mutex
	sink  ProgressSink
	prog  Progress
	order []string
	total map[string]int64
}

func newProgressTracker(sink ProgressSink) *progressTracker {
	return &progressTracker{sink: sink, total: make(map[string]int64)}
}

// addProgress folds one progress packet in and reports the new totals.
func (t *progressTracker) addProgress(rows, bytes, totalRows uint64) {
	t.mu.Lock()
	t.prog.Rows += rows
	t.prog.Bytes += bytes
	if totalRows > t.prog.TotalRows {
		t.prog.TotalRows = totalRows
	}
	snap := t.prog
	t.mu.Unlock()
	t.sink.Progress(snap)
}

// addEvent folds one profile event in: an increment adds to the counter, a
// gauge replaces it. Nothing is reported until flush, so a batch arrives at
// the sink as one consistent set.
func (t *progressTracker) addEvent(name string, gauge bool, value int64) {
	t.mu.Lock()
	if _, seen := t.total[name]; !seen {
		t.order = append(t.order, name)
	}
	if gauge {
		t.total[name] = value
	} else {
		t.total[name] += value
	}
	if name == memoryTrackingEvent {
		t.prog.Memory = t.total[name]
	}
	t.mu.Unlock()
}

// flush reports the events accumulated so far, in the order each was first
// seen, along with the totals — memory may have moved with them.
func (t *progressTracker) flush() {
	t.mu.Lock()
	events := make([]ProfileEvent, len(t.order))
	for i, n := range t.order {
		events[i] = ProfileEvent{Name: n, Value: t.total[n]}
	}
	snap := t.prog
	t.mu.Unlock()
	t.sink.ProfileEvents(events)
	t.sink.Progress(snap)
}
