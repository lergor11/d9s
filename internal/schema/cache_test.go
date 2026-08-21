package schema

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/andreim/d9s/internal/db"
)

// fakeDriver serves canned catalog answers and counts the calls, so a test can
// tell a cache hit from a fresh query.
type fakeDriver struct {
	tables    []db.Table
	cols      map[string][]db.Column
	tablesErr error
	colsErr   error

	tableCalls atomic.Int64
	colCalls   atomic.Int64
}

func (f *fakeDriver) Connect(context.Context, db.Target) error { return nil }

func (f *fakeDriver) ListDatabases(context.Context) ([]db.Database, error) { return nil, nil }

func (f *fakeDriver) ListTables(context.Context) ([]db.Table, error) {
	f.tableCalls.Add(1)
	if f.tablesErr != nil {
		return nil, f.tablesErr
	}
	return f.tables, nil
}

func (f *fakeDriver) ListColumns(_ context.Context, table string) ([]db.Column, error) {
	f.colCalls.Add(1)
	if f.colsErr != nil {
		return nil, f.colsErr
	}
	return f.cols[table], nil
}

func (f *fakeDriver) Execute(_ context.Context, statement string) db.Result {
	return db.Result{Statement: statement, Affected: -1}
}

func (f *fakeDriver) Close() error { return nil }

func sampleDriver() *fakeDriver {
	return &fakeDriver{
		tables: []db.Table{{Name: "users"}, {Name: "orders"}, {Name: "analytics.events"}},
		cols: map[string][]db.Column{
			"users":            {{Name: "id"}, {Name: "email"}},
			"analytics.events": {{Name: "at"}},
		},
	}
}

func TestCacheTablesColdThenReady(t *testing.T) {
	drv := sampleDriver()
	c := New(drv)

	if names, state := c.Tables(); state != Cold || names != nil {
		t.Fatalf("fresh cache = %#v/%v, want nil/Cold", names, state)
	}
	if !c.ClaimTables() {
		t.Fatal("the first claim did not win the fetch")
	}
	if _, state := c.Tables(); state != Loading {
		t.Errorf("state after claiming = %v, want Loading", state)
	}
	if c.ClaimTables() {
		t.Error("a second claim also won the fetch, so the query would run twice")
	}
	if !c.Loading() {
		t.Error("Loading() is false while a claimed fetch has not finished")
	}

	if err := c.LoadTables(context.Background()); err != nil {
		t.Fatalf("LoadTables: %v", err)
	}
	names, state := c.Tables()
	if want := []string{"users", "orders", "analytics.events"}; !reflect.DeepEqual(names, want) {
		t.Errorf("tables = %#v, want %#v", names, want)
	}
	if state != Ready {
		t.Errorf("state = %v, want Ready", state)
	}
	if c.Loading() {
		t.Error("Loading() is still true after the fetch finished")
	}
	if c.ClaimTables() {
		t.Error("a cached listing was claimed again")
	}
	if got := drv.tableCalls.Load(); got != 1 {
		t.Errorf("ListTables called %d times, want 1", got)
	}
}

func TestCacheColumns(t *testing.T) {
	drv := sampleDriver()
	c := New(drv)
	if _, state := c.Columns("users"); state != Cold {
		t.Fatalf("state = %v, want Cold", state)
	}
	if !c.ClaimColumns("users") {
		t.Fatal("the first claim did not win the fetch")
	}
	if c.ClaimColumns("USERS") {
		t.Error("the same table claimed twice under a different case")
	}
	if err := c.LoadColumns(context.Background(), "users"); err != nil {
		t.Fatalf("LoadColumns: %v", err)
	}
	cols, state := c.Columns("Users")
	if want := []string{"id", "email"}; !reflect.DeepEqual(cols, want) {
		t.Errorf("columns = %#v, want %#v", cols, want)
	}
	if state != Ready {
		t.Errorf("state = %v, want Ready", state)
	}
}

func TestCacheResolve(t *testing.T) {
	c := New(sampleDriver())
	if _, ok := c.Resolve("users"); ok {
		t.Error("a cold cache resolved a name it has never listed")
	}
	if !c.ClaimTables() {
		t.Fatal("claim failed")
	}
	if err := c.LoadTables(context.Background()); err != nil {
		t.Fatalf("LoadTables: %v", err)
	}

	tests := []struct {
		name  string
		table string
		want  string
		ok    bool
	}{
		{name: "exact", table: "users", want: "users", ok: true},
		{name: "case-insensitive", table: "USERS", want: "users", ok: true},
		{name: "qualified name kept", table: "analytics.events", want: "analytics.events", ok: true},
		{name: "qualifier the catalog left off", table: "public.users", want: "users", ok: true},
		{name: "leaf of a qualified table", table: "events", want: "analytics.events", ok: true},
		{name: "unknown table", table: "ghosts", want: "", ok: false},
		{name: "empty name", table: "", want: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.Resolve(tt.table)
			if got != tt.want || ok != tt.ok {
				t.Errorf("Resolve(%q) = %q/%v, want %q/%v", tt.table, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCacheLoadErrors(t *testing.T) {
	drv := sampleDriver()
	drv.tablesErr = errors.New("permission denied")
	drv.colsErr = errors.New("no such table")
	c := New(drv)

	c.ClaimTables()
	err := c.LoadTables(context.Background())
	if err == nil || !errors.Is(err, drv.tablesErr) {
		t.Fatalf("LoadTables error = %v, want it to wrap the driver error", err)
	}
	if _, state := c.Tables(); state != Failed {
		t.Errorf("state = %v, want Failed", state)
	}
	if !c.ClaimTables() {
		t.Error("a failed listing cannot be retried")
	}

	c.ClaimColumns("users")
	err = c.LoadColumns(context.Background(), "users")
	if err == nil || !errors.Is(err, drv.colsErr) {
		t.Fatalf("LoadColumns error = %v, want it to wrap the driver error", err)
	}
	if _, state := c.Columns("users"); state != Failed {
		t.Errorf("state = %v, want Failed", state)
	}
}

func TestCacheRefresh(t *testing.T) {
	drv := sampleDriver()
	c := New(drv)
	c.ClaimTables()
	if err := c.LoadTables(context.Background()); err != nil {
		t.Fatalf("LoadTables: %v", err)
	}

	// A table created outside the session only shows up after a refresh.
	drv.tables = append(drv.tables, db.Table{Name: "invoices"})
	c.ClaimColumns("users")
	if err := c.LoadColumns(context.Background(), "users"); err != nil {
		t.Fatalf("LoadColumns: %v", err)
	}
	if names, _ := c.Tables(); len(names) != 3 {
		t.Fatalf("tables = %#v, want the three cached ones", names)
	}

	c.Refresh()
	if _, state := c.Tables(); state != Cold {
		t.Errorf("state after refresh = %v, want Cold", state)
	}
	if _, state := c.Columns("users"); state != Cold {
		t.Errorf("column state after refresh = %v, want Cold", state)
	}
	if !c.ClaimTables() {
		t.Fatal("claim after refresh did not win the fetch")
	}
	if err := c.LoadTables(context.Background()); err != nil {
		t.Fatalf("LoadTables: %v", err)
	}
	names, _ := c.Tables()
	if len(names) != 4 || names[3] != "invoices" {
		t.Errorf("tables after refresh = %#v, want the new table listed", names)
	}
}

func TestCacheConcurrentUse(t *testing.T) {
	drv := sampleDriver()
	c := New(drv)

	var wg sync.WaitGroup
	// Several loaders race for the same names; only the claim winners fetch.
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.ClaimTables() {
				if err := c.LoadTables(context.Background()); err != nil {
					t.Errorf("LoadTables: %v", err)
				}
			}
			if c.ClaimColumns("users") {
				if err := c.LoadColumns(context.Background(), "users"); err != nil {
					t.Errorf("LoadColumns: %v", err)
				}
			}
		}()
	}
	// Readers run the whole time, the way the UI does between key presses.
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				c.Tables()
				c.Columns("users")
				c.Resolve("users")
				c.Loading()
			}
		}()
	}
	wg.Wait()

	if got := drv.tableCalls.Load(); got != 1 {
		t.Errorf("ListTables called %d times, want 1", got)
	}
	if got := drv.colCalls.Load(); got != 1 {
		t.Errorf("ListColumns called %d times, want 1", got)
	}
	if names, state := c.Tables(); state != Ready || len(names) != 3 {
		t.Errorf("tables = %#v/%v, want the three names/Ready", names, state)
	}
}
