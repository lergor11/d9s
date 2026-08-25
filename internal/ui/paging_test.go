package ui

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// pageCursor hands out rows in fixed pages, so a test can watch the interface
// render one page while later ones are still coming.
type pageCursor struct {
	pages     [][][]string
	next      int
	truncated bool
	closed    bool
}

func (c *pageCursor) Columns() []string     { return []string{"n"} }
func (c *pageCursor) ColumnTypes() []string { return []string{"integer"} }
func (c *pageCursor) Done() bool            { return c.next >= len(c.pages) }
func (c *pageCursor) Truncated() bool       { return c.truncated }
func (c *pageCursor) SetCap(int)            {}
func (c *pageCursor) Affected() int64       { return -1 }
func (c *pageCursor) Close() error          { c.closed = true; return nil }

func (c *pageCursor) NextPage(int) ([][]string, error) {
	if c.Done() {
		return nil, nil
	}
	p := c.pages[c.next]
	c.next++
	return p, nil
}

// pagingDriver serves one cursor and remembers it, so the test can assert the
// cursor was closed rather than left pinning a connection.
type pagingDriver struct{ cur *pageCursor }

func (d *pagingDriver) Connect(context.Context, db.Target) error             { return nil }
func (d *pagingDriver) ListDatabases(context.Context) ([]db.Database, error) { return nil, nil }
func (d *pagingDriver) ListTables(context.Context) ([]db.Table, error)       { return nil, nil }
func (d *pagingDriver) ListColumns(context.Context, string) ([]db.Column, error) {
	return nil, nil
}
func (d *pagingDriver) Close() error                              { return nil }
func (d *pagingDriver) Execute(context.Context, string) db.Result { return db.Result{Affected: -1} }

func (d *pagingDriver) ExecuteStream(context.Context, string) (db.Cursor, error) {
	return d.cur, nil
}

func TestRunAppendsPagesAndCarriesTruncation(t *testing.T) {
	cur := &pageCursor{
		pages:     [][][]string{{{"1"}, {"2"}}, {{"3"}}},
		truncated: true,
	}
	m := &model{width: 100, height: 30}
	m.query.open(&pagingDriver{cur: cur}, config.Postgres, "c", "d")
	setEditor(&m.query, "SELECT n FROM t")

	sawPartial := false
	step(t, m, m.startRunOrConfirm(), func() {
		if len(m.query.results) == 1 && len(m.query.results[0].Rows) == 2 && m.query.running {
			sawPartial = true
		}
	})

	if !sawPartial {
		t.Error("the first page never rendered on its own: rows only appeared once the statement finished")
	}
	res := m.query.results[0]
	if len(res.Rows) != 3 {
		t.Errorf("loaded %d rows, want all 3 pages appended", len(res.Rows))
	}
	if !res.Truncated {
		t.Error("Truncated did not reach the result, so the user cannot tell rows were left behind")
	}
	if got := res.ColumnTypes; len(got) != 1 || got[0] != "integer" {
		t.Errorf("column types = %v, want the cursor's", got)
	}
	if !cur.closed {
		t.Error("the cursor was left open, pinning the connection")
	}
}

// step drives a run the way Bubble Tea would, unwrapping the batch that
// carries the spinner alongside the reader, and calling after on every step so
// a test can watch the result grow.
func step(t *testing.T, m *model, cmd tea.Cmd, after func()) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for cmd != nil {
		if time.Now().After(deadline) {
			t.Fatal("the run never finished")
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, sub := range batch {
				// The spinner tick is not part of the run; the reader is.
				if m := sub(); m != nil {
					if _, isTick := m.(spinner.TickMsg); isTick {
						continue
					}
					next := m
					cmd = func() tea.Msg { return next }
				}
			}
			continue
		}
		if _, done := msg.(execDoneMsg); done {
			m.handleExecMsg(msg)
			return
		}
		cmd = m.handleExecMsg(msg)
		after()
	}
}
