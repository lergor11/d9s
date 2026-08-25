package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lergor11/d9s/internal/db"
)

// countingCursor is a db.Cursor over a fixed number of rows that records how
// many it was actually asked to produce, so a test can show the server stops
// reading at the cap instead of draining the result.
type countingCursor struct {
	mu sync.Mutex

	available int // rows the "engine" holds
	served    int // rows handed out so far
	cap       int
	closed    bool
}

func (c *countingCursor) Columns() []string     { return []string{"id"} }
func (c *countingCursor) ColumnTypes() []string { return nil }

func (c *countingCursor) NextPage(n int) ([][]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= 0 {
		n = db.DefaultPageSize
	}
	limit := c.available
	if c.cap > 0 && c.cap < limit {
		limit = c.cap
	}
	var rows [][]string
	for len(rows) < n && c.served < limit {
		c.served++
		rows = append(rows, []string{strings.Repeat("x", 4)})
	}
	return rows, nil
}

func (c *countingCursor) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed || c.served >= c.available || (c.cap > 0 && c.served >= c.cap)
}

func (c *countingCursor) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cap > 0 && c.served >= c.cap && c.available > c.cap
}

func (c *countingCursor) SetCap(rows int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cap = rows
}

func (c *countingCursor) Affected() int64 { return -1 }

func (c *countingCursor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *countingCursor) rowsRead() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.served
}

// streamingDriver is a fakeDriver that also implements db.Streamer, so
// db.Stream pages against it rather than falling back to Execute.
type streamingDriver struct {
	fakeDriver
	cursor *countingCursor
}

func (d *streamingDriver) ExecuteStream(context.Context, string) (db.Cursor, error) {
	return d.cursor, nil
}

// TestQueryStopsReadingAtTheRowCap is the point of streaming: a query matching
// far more rows than the cap must cost one page, not the whole result buffered
// into memory and then discarded.
func TestQueryStopsReadingAtTheRowCap(t *testing.T) {
	const available = 100_000
	cursor := &countingCursor{available: available}
	driver := &streamingDriver{cursor: cursor}

	text, err := runStatement(t.Context(), driver, "SELECT id FROM huge")
	if err != nil {
		t.Fatalf("runStatement: %v", err)
	}
	if got := cursor.rowsRead(); got != MaxRows {
		t.Errorf("the server read %d rows of %d available, want it to stop at the %d-row cap", got, available, MaxRows)
	}
	if !strings.Contains(text, "more remain") {
		t.Errorf("the response does not say rows were held back:\n%s", tail(text))
	}
	if !cursor.closed {
		t.Error("the cursor was left open, which pins the connection it runs on")
	}
}

// TestQueryClosesTheCursorWhenAStatementFails covers the path where paging
// itself errors: the cursor must still be released.
func TestQueryClosesTheCursorWhenAStatementFails(t *testing.T) {
	cursor := &countingCursor{available: 10}
	driver := &streamingDriver{cursor: cursor}
	driver.result = db.Result{Affected: -1}

	if _, err := runStatement(t.Context(), driver, "SELECT id FROM small"); err != nil {
		t.Fatalf("runStatement: %v", err)
	}
	if !cursor.closed {
		t.Error("the cursor was not closed")
	}
}

// TestNonStreamingDriverStillWorks pins the fallback db.Stream provides, which
// is what lets the rest of the tests use a plain fake driver.
func TestNonStreamingDriverStillWorks(t *testing.T) {
	rows := make([][]string, 500)
	for i := range rows {
		rows[i] = []string{"v"}
	}
	driver := &fakeDriver{result: db.Result{Columns: []string{"c"}, Rows: rows, Affected: -1}}

	text, err := runStatement(t.Context(), driver, "SELECT c FROM t")
	if err != nil {
		t.Fatalf("runStatement: %v", err)
	}
	if !strings.Contains(text, "more remain") {
		t.Errorf("the buffered fallback did not report the row cap:\n%s", tail(text))
	}
	if strings.Count(text, "\n") > MaxRows+10 {
		t.Error("the fallback returned more than the row cap")
	}
}
