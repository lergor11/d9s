package db

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// recordingSink keeps the last of what supersedes and all of what accumulates,
// the way the interface does.
type recordingSink struct {
	prog      Progress
	progCalls int
	events    []ProfileEvent
	logs      []LogLine
}

// Progress records the running totals, superseding the previous call.
func (s *recordingSink) Progress(p Progress) { s.prog = p; s.progCalls++ }

// ProfileEvents records the accumulated counters, superseding the previous call.
func (s *recordingSink) ProfileEvents(e []ProfileEvent) { s.events = e }

// Log appends one server log line.
func (s *recordingSink) Log(l LogLine) { s.logs = append(s.logs, l) }

func TestProgressTrackerAccumulates(t *testing.T) {
	tests := []struct {
		name    string
		packets [][3]uint64 // rows, bytes, totalRows per packet
		want    Progress
	}{
		{
			name:    "packets sum, they are deltas",
			packets: [][3]uint64{{100, 1000, 0}, {50, 500, 0}},
			want:    Progress{Rows: 150, Bytes: 1500},
		},
		{
			name:    "the total is absolute and keeps its maximum",
			packets: [][3]uint64{{10, 100, 5000}, {10, 100, 0}, {10, 100, 6000}},
			want:    Progress{Rows: 30, Bytes: 300, TotalRows: 6000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingSink{}
			tr := newProgressTracker(sink)
			for _, p := range tt.packets {
				tr.addProgress(p[0], p[1], p[2])
			}
			if sink.prog != tt.want {
				t.Errorf("progress = %+v, want %+v", sink.prog, tt.want)
			}
			if sink.progCalls != len(tt.packets) {
				t.Errorf("sink heard %d progress calls, want one per packet (%d)", sink.progCalls, len(tt.packets))
			}
		})
	}
}

func TestProgressTrackerEvents(t *testing.T) {
	type ev struct {
		name  string
		gauge bool
		value int64
	}
	tests := []struct {
		name       string
		events     []ev
		want       []ProfileEvent
		wantMemory int64
	}{
		{
			name:   "increments sum across batches",
			events: []ev{{"SelectedRows", false, 100}, {"SelectedRows", false, 50}},
			want:   []ProfileEvent{{Name: "SelectedRows", Value: 150}},
		},
		{
			name:   "gauges keep the last value",
			events: []ev{{"Depth", true, 7}, {"Depth", true, 3}},
			want:   []ProfileEvent{{Name: "Depth", Value: 3}},
		},
		{
			name: "order is first-seen, not alphabetical",
			events: []ev{
				{"SelectedRows", false, 1}, {"NetworkSendBytes", false, 2}, {"SelectedRows", false, 1},
			},
			want: []ProfileEvent{{Name: "SelectedRows", Value: 2}, {Name: "NetworkSendBytes", Value: 2}},
		},
		{
			name:       "MemoryTracking feeds the progress memory figure",
			events:     []ev{{"MemoryTracking", true, 1 << 20}},
			want:       []ProfileEvent{{Name: "MemoryTracking", Value: 1 << 20}},
			wantMemory: 1 << 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingSink{}
			tr := newProgressTracker(sink)
			for _, e := range tt.events {
				tr.addEvent(e.name, e.gauge, e.value)
			}
			tr.flush()
			if !reflect.DeepEqual(sink.events, tt.want) {
				t.Errorf("events = %+v, want %+v", sink.events, tt.want)
			}
			if sink.prog.Memory != tt.wantMemory {
				t.Errorf("memory = %d, want %d", sink.prog.Memory, tt.wantMemory)
			}
		})
	}
}

// plainDriver implements Driver but not ProgressStreamer, standing in for an
// engine with no progress stream.
type plainDriver struct {
	Driver
	executed string
}

// Execute records the statement and returns a canned result.
func (d *plainDriver) Execute(_ context.Context, statement string) Result {
	d.executed = statement
	return Result{Statement: statement, Columns: []string{"a"}, Rows: [][]string{{"1"}}, Affected: -1}
}

func TestStreamProgressFallsBackWithoutAStreamer(t *testing.T) {
	drv := &plainDriver{}
	sink := &recordingSink{}
	cur, err := StreamProgress(context.Background(), drv, "SELECT 1", sink)
	if err != nil {
		t.Fatalf("StreamProgress: %v", err)
	}
	defer func() { _ = cur.Close() }()
	if drv.executed != "SELECT 1" {
		t.Fatalf("the fallback did not run the statement: executed %q", drv.executed)
	}
	rows, err := cur.NextPage(0)
	if err != nil || len(rows) != 1 {
		t.Errorf("NextPage = %v rows, %v; want the buffered row", rows, err)
	}
	if sink.progCalls != 0 || sink.events != nil || sink.logs != nil {
		t.Error("a driver without a progress stream fed the sink anyway")
	}
}

// cancelledStreamer returns a cursor whose fetch fails with the context's
// error, standing in for an engine read cut off mid-scan.
type cancelledStreamer struct {
	Driver
	tracker **progressTracker
}

// ExecuteStream hands back a cursor over a source that reports progress for
// one page and then finds the context cancelled.
func (d *cancelledStreamer) ExecuteStream(ctx context.Context, _ string) (Cursor, error) {
	return newCursor(ctx, []string{"n"}, 0, &cancellingSource{ctx: ctx, tracker: d.tracker}), nil
}

type cancellingSource struct {
	ctx     context.Context
	tracker **progressTracker
	fetched bool
}

// fetch reports progress, then fails with the cancel on the second call.
func (s *cancellingSource) fetch(int) ([][]string, bool, error) {
	if !s.fetched {
		s.fetched = true
		(*s.tracker).addProgress(500, 4096, 1000)
		return [][]string{{"1"}}, false, nil
	}
	return nil, true, s.ctx.Err()
}

func (s *cancellingSource) release() error { return nil }

func (s *cancellingSource) affected() int64 { return -1 }

func TestProgressSurvivesCancellation(t *testing.T) {
	sink := &recordingSink{}
	var tr *progressTracker
	drv := &cancelledStreamer{tracker: &tr}
	tr = newProgressTracker(sink)

	ctx, cancel := context.WithCancel(context.Background())
	cur, err := drv.ExecuteStream(ctx, "SELECT n FROM big")
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if _, err := cur.NextPage(0); err != nil {
		t.Fatalf("first page: %v", err)
	}
	cancel()
	if _, err := cur.NextPage(0); !errors.Is(err, context.Canceled) {
		t.Fatalf("second page error = %v, want the cancellation", err)
	}
	want := Progress{Rows: 500, Bytes: 4096, TotalRows: 1000}
	if sink.prog != want {
		t.Errorf("counters after cancel = %+v, want %+v preserved", sink.prog, want)
	}
}
