// Package db defines the engine-agnostic driver contract and shared result
// model. The UI depends only on this package, never on concrete engines.
package db

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/lergor11/d9s/internal/config"
)

// DialContextFunc dials a raw TCP connection to the database host. When the
// connection is tunneled, this dials through the SSH client instead of the OS.
type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Database is one selectable database inside a connection.
type Database struct {
	Name   string
	Detail string // e.g. owner+size (postgres), engine (clickhouse), key count (redis)
}

// Result is the outcome of a single statement.
type Result struct {
	Statement string
	Columns   []string
	// ColumnTypes are the engine's type names, positionally matching Columns.
	// A driver that does not report them leaves this nil, and the interface
	// shows the column names alone.
	ColumnTypes []string
	Rows        [][]string
	Affected    int64 // -1 when not applicable
	Err         error
	Skipped     bool // statement not run because a previous one failed / cancelled
	// Truncated reports that the row cap stopped the read, so the statement
	// has more rows than Rows holds.
	Truncated bool
	Duration  time.Duration
}

// Target identifies what to connect to: a configured connection plus resolved
// secrets and an optional dialer that routes through an SSH tunnel.
type Target struct {
	Config   config.Connection
	Password string          // already resolved; empty if none
	Dial     DialContextFunc // nil = direct TCP
	Database string          // selected database; empty = engine default
	Secrets  SecretResolver  // resolves op:// TLS material; nil = paths only
}

// Table is one browsable container in the current database: a SQL table or,
// for Redis, a key prefix.
type Table struct {
	Name   string
	Detail string // e.g. row estimate (SQL) or key count (Redis)
}

// Column describes one field of a table, or one key inside a Redis prefix.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	Detail   string // e.g. default value, key ordinal, or TTL
}

// Driver is implemented once per engine.
type Driver interface {
	// Connect establishes the session. Implementations must be safe to call
	// from a non-UI goroutine.
	Connect(ctx context.Context, t Target) error
	// ListDatabases enumerates databases available on the connection.
	ListDatabases(ctx context.Context) ([]Database, error)
	// ListTables enumerates the tables of the connected database; for Redis it
	// enumerates key prefixes.
	ListTables(ctx context.Context) ([]Table, error)
	// ListColumns describes one table returned by ListTables; for Redis it
	// lists the keys under a prefix.
	ListColumns(ctx context.Context, table string) ([]Column, error)
	// Execute runs one statement and returns its result. Err is recorded in
	// the Result rather than returned, except for context cancellation.
	Execute(ctx context.Context, statement string) Result
	// Close releases the session and its underlying network resources.
	Close() error
}

// Factory creates a fresh driver instance for a connection.
type Factory func() Driver

var registry = map[config.EngineType]Factory{}

// Register makes an engine available to the UI. Called from adapter init().
func Register(t config.EngineType, f Factory) { registry[t] = f }

// New returns a driver for the engine type.
func New(t config.EngineType) (Driver, error) {
	f, ok := registry[t]
	if !ok {
		return nil, fmt.Errorf("no driver registered for engine %q", t)
	}
	return f(), nil
}
