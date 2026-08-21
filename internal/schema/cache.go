// Package schema keeps a per-session cache of the names a query editor can
// complete: the tables of the connected database and the columns of each
// table. Catalog queries run through the session driver, so every fetch is
// meant to happen inside a background command and never on the UI goroutine.
package schema

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/lergor11/d9s/internal/db"
)

// State is how much the cache knows about one set of names.
type State int

const (
	// Cold means the names have not been requested yet.
	Cold State = iota
	// Loading means a catalog query for them is in flight.
	Loading
	// Ready means the names are cached and usable.
	Ready
	// Failed means the last catalog query for them returned an error.
	Failed
)

// entry is one cached set of names plus its state.
type entry struct {
	names []string
	state State
}

// Cache stores the table and column names of one session. Every method is safe
// for concurrent use: the reading methods only take a mutex, so the UI can call
// them without blocking, while the Load methods perform the catalog query and
// belong in a background command.
//
// A fetch is a pair: Claim reserves it on the UI goroutine so a second Tab
// press does not start the same query twice, and Load then runs it. Loads are
// serialized against each other because a session driver such as postgres
// holds a single connection that cannot be shared.
type Cache struct {
	driver db.Driver

	mu     sync.Mutex
	tables entry
	cols   map[string]entry

	call sync.Mutex // held for the duration of a driver call
}

// New returns an empty cache backed by the session driver.
func New(driver db.Driver) *Cache {
	return &Cache{driver: driver, cols: map[string]entry{}}
}

// Tables returns the cached table names in catalog order and the state of the
// table listing. For Redis the names are key prefixes.
func (c *Cache) Tables() ([]string, State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tables.names, c.tables.state
}

// Columns returns the cached column names of table and the state of that
// entry. For Redis, table is a key prefix and the names are the keys under it.
func (c *Cache) Columns(table string) ([]string, State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.cols[canonical(table)]
	return e.names, e.state
}

// Resolve maps a table name written in a statement to the name the cache knows
// it by, matching case-insensitively and tolerating a schema qualifier that the
// catalog leaves off. The bool reports whether the table listing is loaded and
// holds a matching name.
func (c *Cache) Resolve(table string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tables.state != Ready {
		return "", false
	}
	want := canonical(table)
	for _, name := range c.tables.names {
		if canonical(name) == want {
			return name, true
		}
	}
	wantLeaf := leaf(want)
	for _, name := range c.tables.names {
		if leaf(canonical(name)) == wantLeaf {
			return name, true
		}
	}
	return "", false
}

// ClaimTables reserves the table listing and reports whether the caller must
// now run LoadTables. It returns false when the listing is already cached or
// another caller is loading it.
func (c *Cache) ClaimTables() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tables.state == Loading || c.tables.state == Ready {
		return false
	}
	c.tables.state = Loading
	return true
}

// ClaimColumns reserves the column listing of table and reports whether the
// caller must now run LoadColumns for it.
func (c *Cache) ClaimColumns(table string) bool {
	key := canonical(table)
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.cols[key]; e.state == Loading || e.state == Ready {
		return false
	}
	c.cols[key] = entry{state: Loading}
	return true
}

// LoadTables lists the tables through the driver and caches their names. It
// issues a catalog query, so it must run off the UI goroutine and only after
// ClaimTables returned true.
func (c *Cache) LoadTables(ctx context.Context) error {
	c.call.Lock()
	tables, err := c.driver.ListTables(ctx)
	c.call.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.tables = entry{state: Failed}
		return fmt.Errorf("listing tables: %w", err)
	}
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.Name)
	}
	c.tables = entry{names: names, state: Ready}
	return nil
}

// LoadColumns describes one table through the driver and caches its column
// names. Like LoadTables it issues a catalog query and must run off the UI
// goroutine, only after ClaimColumns returned true for the same table.
func (c *Cache) LoadColumns(ctx context.Context, table string) error {
	c.call.Lock()
	cols, err := c.driver.ListColumns(ctx, table)
	c.call.Unlock()

	key := canonical(table)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.cols[key] = entry{state: Failed}
		return fmt.Errorf("describing %s: %w", table, err)
	}
	names := make([]string, 0, len(cols))
	for _, col := range cols {
		names = append(names, col.Name)
	}
	c.cols[key] = entry{names: names, state: Ready}
	return nil
}

// Loading reports whether any catalog query started through this cache is
// still in flight.
func (c *Cache) Loading() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tables.state == Loading {
		return true
	}
	for _, e := range c.cols {
		if e.state == Loading {
			return true
		}
	}
	return false
}

// Refresh drops every cached name, so the next completion reloads them. Loads
// already in flight keep their claim and still store their result; the names
// they bring back are the fresh ones.
func (c *Cache) Refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tables.state != Loading {
		c.tables = entry{}
	}
	for table, e := range c.cols {
		if e.state != Loading {
			delete(c.cols, table)
		}
	}
}

// canonical folds a table name to the form the cache keys and compares on.
func canonical(table string) string {
	return strings.ToLower(strings.TrimSpace(table))
}

// leaf drops a schema qualifier, so `analytics.events` compares as `events`.
func leaf(table string) string {
	if _, name, ok := strings.Cut(table, "."); ok {
		return name
	}
	return table
}
