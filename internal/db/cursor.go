package db

import (
	"context"
	"sync"
	"time"
)

// DefaultPageSize is how many rows NextPage returns when asked for a
// non-positive number.
const DefaultPageSize = 200

// Cursor streams the rows of one statement, holding the engine's result open
// between pages so a large query renders its first rows without buffering the
// rest. Every cursor must be closed: an open one pins the connection it runs
// on.
//
// A cursor is not meant to be read from several goroutines at once, but Close
// is safe to call from another, and happens on its own when the context that
// produced the cursor is cancelled.
//
// Close and cancellation are not interchangeable. Close lets go of the result
// and leaves the session usable, so it is what to call when the user navigates
// away from a result with rows still unread. Cancelling the context aborts the
// query itself, which for postgres costs the connection: pgx closes a
// connection whose result stream it had to abandon mid-protocol. That is
// exactly what a cancelled Execute has always done, so cancellation remains
// the right way to stop a runaway query and the wrong way to stop reading one.
type Cursor interface {
	// Columns names the result columns. It is valid before the first page.
	Columns() []string
	// ColumnTypes are the engine's type names, positionally matching Columns,
	// or nil from an engine that does not report them.
	ColumnTypes() []string
	// NextPage returns up to n more rows: fewer at the end of the result or at
	// the cap, and none once Done reports true. A non-positive n asks for
	// DefaultPageSize. A cursor whose context was cancelled returns that error
	// here, so an abandoned read is not mistaken for a finished one.
	NextPage(n int) ([][]string, error)
	// Done reports that NextPage has nothing more to give, because the result
	// ran out, the cap was reached, or the cursor was closed.
	Done() bool
	// Truncated reports that Done is true only because the cap was reached, so
	// rows remain that raising the cap would reach.
	Truncated() bool
	// SetCap replaces the row cap, counted over the life of the cursor. Zero
	// or less means no cap. Raising it above the rows already loaded clears
	// Truncated and lets NextPage carry on.
	SetCap(rows int)
	// Affected is the number of rows a statement changed, or -1 when that does
	// not apply. It is only meaningful once the result is drained, which on
	// some engines means after Close.
	Affected() int64
	// Close releases the engine's result. It is idempotent.
	Close() error
}

// Streamer is implemented by drivers that can page a result set. It is kept
// out of Driver so a driver, or a test double, can exist without one; reach
// for Stream rather than asserting on this directly.
type Streamer interface {
	// ExecuteStream runs one statement and returns a cursor over its rows,
	// leaving the engine's result open until the cursor is closed.
	ExecuteStream(ctx context.Context, statement string) (Cursor, error)
}

// Stream pages the result of one statement. A driver that implements Streamer
// pages for real; any other driver runs the statement through Execute and
// serves the buffered rows as a single page, so a caller needs only one path.
func Stream(ctx context.Context, d Driver, statement string) (Cursor, error) {
	if s, ok := d.(Streamer); ok {
		return s.ExecuteStream(ctx, statement)
	}
	res := d.Execute(ctx, statement)
	if res.Err != nil {
		return nil, res.Err
	}
	return newRowCursor(res.Columns, res.Rows, res.Affected), nil
}

// rowSource is the engine-specific half of a cursor: it yields rows and frees
// whatever the engine holds open.
type rowSource interface {
	// fetch returns up to n rows, reporting true once the underlying result
	// has no more to give. Rows already read are returned alongside an error.
	fetch(n int) (rows [][]string, exhausted bool, err error)
	// release frees the engine's result. The cursor calls it once.
	release() error
	// affected is the number of rows the statement changed, or -1.
	affected() int64
}

// typedSource is a rowSource that knows the engine's column type names. It is
// optional: a source without it yields a cursor whose ColumnTypes is nil, the
// same as an engine that does not report types.
type typedSource interface {
	columnTypes() []string
}

// cursor carries the paging, capping and lifetime rules every engine shares,
// so an adapter only has to supply a rowSource.
type cursor struct {
	mu        sync.Mutex
	columns   []string
	src       rowSource
	rowCap    int
	loaded    int
	exhausted bool
	closed    bool
	err       error
	stopWatch func() bool
}

// newCursor wraps an engine result. Cancelling ctx releases it: a pgx or
// clickhouse rows left open holds its connection until the process exits.
func newCursor(ctx context.Context, columns []string, rowCap int, src rowSource) *cursor {
	c := &cursor{columns: columns, src: src, rowCap: rowCap}
	c.stopWatch = context.AfterFunc(ctx, func() { c.cancel(ctx.Err()) })
	return c
}

// cancel closes the cursor and remembers why, so a reader is told the query was
// abandoned rather than being handed the empty page that means "finished".
func (c *cursor) cancel(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
	_ = c.closeLocked()
}

// newRowCursor serves rows that are already in memory. There is no engine
// resource to release, so no context watcher, and no cap either: capping rows
// that have already been paid for would hide them without saving anything.
func newRowCursor(columns []string, rows [][]string, affected int64) *cursor {
	return &cursor{
		columns: columns,
		src:     &sliceSource{rows: rows, affectedRows: affected},
	}
}

func (c *cursor) Columns() []string { return c.columns }

func (c *cursor) ColumnTypes() []string {
	if t, ok := c.src.(typedSource); ok {
		return t.columnTypes()
	}
	return nil
}

func (c *cursor) NextPage(n int) ([][]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	if c.done() {
		return nil, nil
	}
	if n <= 0 {
		n = DefaultPageSize
	}
	if c.rowCap > 0 {
		if room := c.rowCap - c.loaded; n > room {
			n = room
		}
	}
	rows, exhausted, err := c.src.fetch(n)
	c.loaded += len(rows)
	if exhausted {
		c.exhausted = true
	}
	if err != nil {
		c.err = err
		_ = c.closeLocked()
		return nil, err
	}
	return rows, nil
}

func (c *cursor) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done()
}

// done is Done without the lock, for callers that already hold it.
func (c *cursor) done() bool {
	return c.exhausted || c.closed || c.capped()
}

// capped reports whether the cap has been reached.
func (c *cursor) capped() bool { return c.rowCap > 0 && c.loaded >= c.rowCap }

func (c *cursor) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.exhausted && c.capped()
}

func (c *cursor) SetCap(rows int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rowCap = rows
}

func (c *cursor) Affected() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.src.affected()
}

func (c *cursor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

// closeLocked releases the source once, with the lock already held. Holding it
// is what keeps a cancellation from closing the engine's rows underneath a
// fetch that is still running.
func (c *cursor) closeLocked() error {
	if c.closed {
		return nil
	}
	c.closed = true
	if c.stopWatch != nil {
		c.stopWatch()
	}
	return c.src.release()
}

// sliceSource serves rows that are already materialised.
type sliceSource struct {
	rows         [][]string
	affectedRows int64
}

// fetch hands back every row at once: there is nothing to page, so honouring n
// would only pretend otherwise.
func (s *sliceSource) fetch(int) ([][]string, bool, error) {
	rows := s.rows
	s.rows = nil
	return rows, true, nil
}

func (s *sliceSource) release() error { s.rows = nil; return nil }

func (s *sliceSource) affected() int64 { return s.affectedRows }

// executeViaCursor drains a cursor into a Result, so the one-shot and paged
// paths share one implementation and cannot drift apart. The cap is lifted
// first: Execute has always returned the whole result, and the CLI, the MCP
// server and export all depend on that.
func executeViaCursor(ctx context.Context, s Streamer, statement string) (res Result) {
	res.Statement = statement
	res.Affected = -1
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	cur, err := s.ExecuteStream(ctx, statement)
	if err != nil {
		res.Err = err
		return res
	}
	defer func() { _ = cur.Close() }()
	cur.SetCap(0)
	res.Columns = cur.Columns()
	for !cur.Done() {
		rows, err := cur.NextPage(DefaultPageSize)
		if err != nil {
			res.Err = err
			return res
		}
		if len(rows) == 0 {
			// Nothing left, even though the source has not said so. Break
			// rather than spin, in case an adapter ever reports it that way.
			break
		}
		res.Rows = append(res.Rows, rows...)
	}
	// Close before reading the count: pgx only fills the command tag once the
	// rows are closed.
	if err := cur.Close(); err != nil {
		res.Err = err
		return res
	}
	res.Affected = cur.Affected()
	return res
}
