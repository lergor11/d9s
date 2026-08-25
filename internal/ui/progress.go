package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/lergor11/d9s/internal/db"
)

// runProgress collects what the engine reports for the statement now running.
// The driver's goroutine writes it and the UI goroutine reads it on spinner
// ticks, so everything goes through the mutex. It implements db.ProgressSink.
type runProgress struct {
	mu     sync.Mutex
	seen   bool
	prog   db.Progress
	events []db.ProfileEvent
	logs   []db.LogLine
}

// Progress stores the running totals, superseding the previous call.
func (r *runProgress) Progress(p db.Progress) {
	r.mu.Lock()
	r.prog, r.seen = p, true
	r.mu.Unlock()
}

// ProfileEvents stores the accumulated counters, superseding the previous call.
func (r *runProgress) ProfileEvents(e []db.ProfileEvent) {
	r.mu.Lock()
	r.events = e
	r.mu.Unlock()
}

// Log appends one server log line.
func (r *runProgress) Log(l db.LogLine) {
	r.mu.Lock()
	r.logs = append(r.logs, l)
	r.mu.Unlock()
}

// snapshot is the live counters, and whether the engine has reported any.
func (r *runProgress) snapshot() (db.Progress, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prog, r.seen
}

// take returns everything collected for the finished statement and clears the
// sink for the next one.
func (r *runProgress) take() (read *db.Progress, events []db.ProfileEvent, logs []db.LogLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen {
		p := r.prog
		read = &p
	}
	events, logs = r.events, r.logs
	r.seen, r.prog, r.events, r.logs = false, db.Progress{}, nil, nil
	return read, events, logs
}

// progressStatus is the live part of the status line while a run is on: rows,
// bytes, a percentage when the engine gave a total, memory, and elapsed time —
// or elapsed alone for an engine that reports nothing.
func (q *queryModel) progressStatus() string {
	var parts []string
	if q.prog != nil {
		if p, ok := q.prog.snapshot(); ok {
			parts = append(parts, fmtCount(p.Rows)+" rows", fmtBytes(p.Bytes))
			if p.TotalRows > 0 {
				parts = append(parts, fmt.Sprintf("%d%%", min(100, p.Rows*100/p.TotalRows)))
			}
			if p.Memory > 0 {
				parts = append(parts, "mem "+fmtBytes(uint64(p.Memory)))
			}
		}
	}
	if !q.stmtStart.IsZero() {
		parts = append(parts, fmt.Sprintf("%.1fs", time.Since(q.stmtStart).Seconds()))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " · ")
}

// readNote says what the engine read for a finished statement, used in the
// final line of a result section. Empty when the engine reported nothing.
func readNote(r db.Result) string {
	if r.Read == nil {
		return ""
	}
	return fmt.Sprintf("read %s rows (%s)", fmtCount(r.Read.Rows), fmtBytes(r.Read.Bytes))
}

// renderResultLogs renders a statement's server log lines, one per line,
// tagged with their levels.
func renderResultLogs(r db.Result, width int) string {
	if len(r.Logs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range r.Logs {
		line := fmt.Sprintf("[%s] %s: %s", l.Level, l.Source, l.Text)
		b.WriteString(stDim.Render(truncate(line, max(width-2, 10))) + "\n")
	}
	return b.String()
}

// openProfile shows the focused statement's profile events full-screen,
// through the same overlay the cell inspector uses.
func (m *model) openProfile() {
	q := &m.query
	res, idx, ok := q.focusedResult()
	if !ok {
		m.status = "no result to profile"
		return
	}
	width, height := m.inspectorSize()
	body := profileBody(res)
	vp := viewport.New(width, height)
	vp.SetContent(body)
	q.inspect = inspectModel{
		open:  true,
		title: fmt.Sprintf("profile · statement %d", idx),
		raw:   body,
		vp:    vp,
	}
}

// profileBody lists a statement's profile events one per row, or says the
// engine reported none rather than showing an empty table.
func profileBody(r db.Result) string {
	if len(r.ProfileEvents) == 0 {
		return "the engine reported no profile events for this statement"
	}
	nameW := 0
	for _, e := range r.ProfileEvents {
		if n := len(e.Name); n > nameW {
			nameW = n
		}
	}
	var b strings.Builder
	for _, e := range r.ProfileEvents {
		fmt.Fprintf(&b, "%s %d\n", pad(e.Name, nameW), e.Value)
	}
	return strings.TrimRight(b.String(), "\n")
}

// fmtCount renders a row count compactly: 950, 12.3k, 1.23M, 4.56B.
func fmtCount(n uint64) string {
	switch {
	case n >= 1e9:
		return trimZero(fmt.Sprintf("%.2f", float64(n)/1e9)) + "B"
	case n >= 1e6:
		return trimZero(fmt.Sprintf("%.2f", float64(n)/1e6)) + "M"
	case n >= 1e3:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1e3)) + "k"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// fmtBytes renders a byte count with a binary unit: 512 B, 350.5 MB.
func fmtBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return trimZero(fmt.Sprintf("%.1f", float64(n)/float64(div))) + " " + string("KMGTPE"[exp]) + "B"
}

// trimZero drops a trailing ".0" or ".00" left by fixed-precision formatting.
func trimZero(s string) string {
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
