package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// countingSource stands in for an engine result: it yields numbered rows and
// records how it was driven, so a test can tell a released cursor from a
// leaked one.
type countingSource struct {
	mu        sync.Mutex
	total     int    // rows available in all
	served    int    // rows handed out so far
	releases  int    // times release was called
	failAt    int    // fail the fetch that would pass this many served rows; 0 = never
	lazyEnd   bool   // discover the end only by failing to fill a page, as pgx does
	onRelease func() // called once, inside release
}

func (s *countingSource) fetch(n int) ([][]string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, 0, n)
	for len(out) < n && s.served < s.total {
		if s.failAt > 0 && s.served >= s.failAt {
			return out, false, errors.New("engine went away")
		}
		out = append(out, []string{strconv.Itoa(s.served)})
		s.served++
	}
	if s.lazyEnd {
		// A pgx or clickhouse result only reveals the end when a read comes
		// up short, so a page filled exactly leaves the question open.
		return out, len(out) < n, nil
	}
	return out, s.served >= s.total, nil
}

func (s *countingSource) release() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases++
	if s.onRelease != nil {
		s.onRelease()
		s.onRelease = nil
	}
	return nil
}

func (s *countingSource) affected() int64 { return -1 }

func (s *countingSource) counts() (served, releases int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served, s.releases
}

// drain reads pages of the given size until the cursor is done.
func drain(t *testing.T, c Cursor, page int) [][]string {
	t.Helper()
	var rows [][]string
	for !c.Done() {
		got, err := c.NextPage(page)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}
		if len(got) == 0 {
			break
		}
		rows = append(rows, got...)
	}
	return rows
}

func TestCursorPagesUpToTheCap(t *testing.T) {
	tests := []struct {
		name          string
		total         int
		rowCap        int
		page          int
		lazyEnd       bool
		wantRows      int
		wantTruncated bool
	}{
		{
			name:  "result shorter than the cap is exhausted",
			total: 7, rowCap: 100, page: 3, wantRows: 7,
		},
		{
			name:  "result longer than the cap stops at it",
			total: 50, rowCap: 10, page: 3, wantRows: 10, wantTruncated: true,
		},
		{
			// The source reported the end in the same read that reached the
			// cap, so there is nothing more and nothing to announce.
			name:  "a source that reports the end at the cap is not truncated",
			total: 10, rowCap: 10, page: 4, wantRows: 10,
		},
		{
			// This source only learns the end by coming up short, so filling
			// the cap exactly leaves rows possibly remaining: say so rather
			// than claim an exhaustion nobody has established.
			name:  "a cap reached before the end is known reports truncated",
			total: 10, rowCap: 10, page: 5, lazyEnd: true, wantRows: 10, wantTruncated: true,
		},
		{
			name:  "a page larger than the cap is clamped to it",
			total: 50, rowCap: 10, page: 999, wantRows: 10, wantTruncated: true,
		},
		{
			name:  "no cap reads everything",
			total: 25, rowCap: 0, page: 10, wantRows: 25,
		},
		{
			name:  "empty result is exhausted, not truncated",
			total: 0, rowCap: 10, page: 5, wantRows: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &countingSource{total: tt.total, lazyEnd: tt.lazyEnd}
			c := newCursor(context.Background(), []string{"n"}, tt.rowCap, src)
			t.Cleanup(func() { _ = c.Close() })

			rows := drain(t, c, tt.page)
			if len(rows) != tt.wantRows {
				t.Errorf("read %d rows, want %d", len(rows), tt.wantRows)
			}
			if !c.Done() {
				t.Error("cursor is not done after draining")
			}
			if got := c.Truncated(); got != tt.wantTruncated {
				t.Errorf("Truncated() = %v, want %v", got, tt.wantTruncated)
			}
			// A capped cursor must not have pulled more rows out of the engine
			// than it handed back.
			if served, _ := src.counts(); served != tt.wantRows {
				t.Errorf("the source served %d rows for %d returned", served, tt.wantRows)
			}
			if got, err := c.NextPage(tt.page); err != nil || len(got) != 0 {
				t.Errorf("NextPage past the end = %v, %v; want no rows and no error", got, err)
			}
		})
	}
}

func TestCursorRaisingTheCapResumes(t *testing.T) {
	src := &countingSource{total: 25}
	c := newCursor(context.Background(), []string{"n"}, 10, src)
	t.Cleanup(func() { _ = c.Close() })

	if rows := drain(t, c, 4); len(rows) != 10 {
		t.Fatalf("first pass read %d rows, want the cap of 10", len(rows))
	}
	if !c.Truncated() {
		t.Fatal("cursor is not truncated at the cap")
	}

	c.SetCap(20)
	if c.Done() {
		t.Error("cursor is still done after the cap was raised")
	}
	if rows := drain(t, c, 4); len(rows) != 10 {
		t.Errorf("second pass read %d rows, want 10 more", len(rows))
	}
	if !c.Truncated() {
		t.Error("cursor is not truncated at the raised cap")
	}

	c.SetCap(0)
	if rows := drain(t, c, 4); len(rows) != 5 {
		t.Errorf("final pass read %d rows, want the remaining 5", len(rows))
	}
	if c.Truncated() {
		t.Error("cursor reports truncated after reading every row")
	}
	if !c.Done() {
		t.Error("cursor is not done after the result was exhausted")
	}
}

func TestCursorTruncationResolvesWhenTheCallerContinues(t *testing.T) {
	// A cap reached before the end is known reports truncated; carrying on
	// finds no more rows and settles into plain exhaustion, so the interface
	// corrects itself rather than promising rows that never come.
	src := &countingSource{total: 10, lazyEnd: true}
	c := newCursor(context.Background(), []string{"n"}, 10, src)
	t.Cleanup(func() { _ = c.Close() })

	if rows := drain(t, c, 10); len(rows) != 10 {
		t.Fatalf("read %d rows, want 10", len(rows))
	}
	if !c.Truncated() {
		t.Fatal("cursor is not truncated at a cap reached before the end was known")
	}

	c.SetCap(0)
	rows, err := c.NextPage(10)
	if err != nil {
		t.Fatalf("NextPage: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("continuing produced %d rows, want none", len(rows))
	}
	if !c.Done() || c.Truncated() {
		t.Errorf("Done() = %v, Truncated() = %v; want done and no longer truncated", c.Done(), c.Truncated())
	}
}

func TestCursorCloseReleasesOnceAndStopsPaging(t *testing.T) {
	src := &countingSource{total: 100}
	c := newCursor(context.Background(), []string{"n"}, 0, src)

	if _, err := c.NextPage(5); err != nil {
		t.Fatalf("NextPage: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	served, releases := src.counts()
	if releases != 1 {
		t.Errorf("the source was released %d times, want exactly 1", releases)
	}
	if !c.Done() {
		t.Error("a closed cursor is not done")
	}
	if c.Truncated() {
		t.Error("closing early reported the result as truncated by the cap")
	}
	if got, err := c.NextPage(5); err != nil || len(got) != 0 {
		t.Errorf("NextPage after Close = %v, %v; want no rows and no error", got, err)
	}
	if now, _ := src.counts(); now != served {
		t.Errorf("the source served %d more rows after Close", now-served)
	}
}

func TestCursorCancellationReleasesTheResult(t *testing.T) {
	released := make(chan struct{})
	src := &countingSource{total: 100, onRelease: func() { close(released) }}
	ctx, cancel := context.WithCancel(context.Background())
	c := newCursor(ctx, []string{"n"}, 0, src)
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.NextPage(5); err != nil {
		t.Fatalf("NextPage: %v", err)
	}
	cancel()

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not release the engine result")
	}
	if !c.Done() {
		t.Error("cursor is not done after cancellation")
	}
	if _, releases := src.counts(); releases != 1 {
		t.Errorf("the source was released %d times, want exactly 1", releases)
	}
	// An abandoned read must not read like a finished one.
	if _, err := c.NextPage(5); !errors.Is(err, context.Canceled) {
		t.Errorf("NextPage after cancellation = %v, want context.Canceled", err)
	}
}

func TestCursorFetchFailureClosesTheCursor(t *testing.T) {
	src := &countingSource{total: 100, failAt: 6}
	c := newCursor(context.Background(), []string{"n"}, 0, src)

	if _, err := c.NextPage(6); err != nil {
		t.Fatalf("first NextPage: %v", err)
	}
	_, err := c.NextPage(6)
	if err == nil {
		t.Fatal("NextPage succeeded past a broken source, want an error")
	}
	if _, releases := src.counts(); releases != 1 {
		t.Errorf("the source was released %d times after a failure, want 1", releases)
	}
	// The error sticks, so a caller that keeps scrolling keeps seeing it.
	if _, again := c.NextPage(6); again == nil {
		t.Error("the error was forgotten on the next page")
	}
}

func TestNewRowCursorServesEverythingAtOnce(t *testing.T) {
	rows := [][]string{{"a"}, {"b"}, {"c"}}
	c := newRowCursor([]string{"reply"}, rows, -1)
	t.Cleanup(func() { _ = c.Close() })

	// A materialised reply ignores the page size: there is nothing to page.
	got, err := c.NextPage(1)
	if err != nil {
		t.Fatalf("NextPage: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("first page has %d rows, want all 3", len(got))
	}
	if !c.Done() || c.Truncated() {
		t.Errorf("Done() = %v, Truncated() = %v; want done and not truncated", c.Done(), c.Truncated())
	}
	if got := c.Columns(); len(got) != 1 || got[0] != "reply" {
		t.Errorf("Columns() = %v, want [reply]", got)
	}
}

// capStreamer is a Streamer whose cursors carry a cap, for checking that the
// one-shot path lifts it.
type capStreamer struct {
	rowCap, total int
	err           error
}

func (s *capStreamer) ExecuteStream(ctx context.Context, _ string) (Cursor, error) {
	if s.err != nil {
		return nil, s.err
	}
	return newCursor(ctx, []string{"n"}, s.rowCap, &countingSource{total: s.total}), nil
}

func TestExecuteViaCursorIgnoresTheCap(t *testing.T) {
	// Execute has always returned the whole result, and export and the CLI
	// depend on that, so the cap must not reach it.
	s := &capStreamer{rowCap: 10, total: 250}

	res := executeViaCursor(context.Background(), s, "SELECT 1")
	if res.Err != nil {
		t.Fatalf("Execute: %v", res.Err)
	}
	if len(res.Rows) != 250 {
		t.Errorf("got %d rows, want all 250 despite the cap of 10", len(res.Rows))
	}
	if res.Statement != "SELECT 1" {
		t.Errorf("Statement = %q, want the statement echoed back", res.Statement)
	}
	if res.Affected != -1 {
		t.Errorf("Affected = %d, want -1 for a statement returning rows", res.Affected)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "n" {
		t.Errorf("Columns = %v, want [n]", res.Columns)
	}
	if res.Duration <= 0 {
		t.Error("Duration was not measured")
	}
}

func TestExecuteViaCursorReportsAStartupFailure(t *testing.T) {
	wantErr := errors.New("syntax error")
	s := &capStreamer{err: wantErr}

	res := executeViaCursor(context.Background(), s, "SELEKT 1")
	if !errors.Is(res.Err, wantErr) {
		t.Errorf("Err = %v, want it to carry %v in the result rather than panicking", res.Err, wantErr)
	}
	if res.Affected != -1 || len(res.Rows) != 0 {
		t.Errorf("got Affected=%d and %d rows, want -1 and none", res.Affected, len(res.Rows))
	}
}

// bufferedDriver implements Driver but not Streamer, standing in for the test
// doubles other packages use.
type bufferedDriver struct{ res Result }

func (d *bufferedDriver) Connect(context.Context, Target) error { return nil }
func (d *bufferedDriver) ListDatabases(context.Context) ([]Database, error) {
	return nil, nil
}
func (d *bufferedDriver) ListTables(context.Context) ([]Table, error) { return nil, nil }
func (d *bufferedDriver) ListColumns(context.Context, string) ([]Column, error) {
	return nil, nil
}
func (d *bufferedDriver) Execute(context.Context, string) Result { return d.res }
func (d *bufferedDriver) Close() error                           { return nil }

func TestStreamFallsBackToExecute(t *testing.T) {
	rows := make([][]string, 12)
	for i := range rows {
		rows[i] = []string{fmt.Sprint(i)}
	}
	d := &bufferedDriver{res: Result{Columns: []string{"n"}, Rows: rows, Affected: -1}}

	cur, err := Stream(context.Background(), d, "SELECT 1")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close() })
	got := drain(t, cur, 5)
	if len(got) != 12 {
		t.Errorf("read %d rows, want the 12 Execute buffered", len(got))
	}
	if cur.Truncated() {
		t.Error("a buffered result was reported truncated")
	}
}

func TestStreamReportsAnExecuteFailure(t *testing.T) {
	wantErr := errors.New("no such table")
	d := &bufferedDriver{res: Result{Err: wantErr}}

	if _, err := Stream(context.Background(), d, "SELECT 1"); !errors.Is(err, wantErr) {
		t.Errorf("Stream error = %v, want %v", err, wantErr)
	}
}
