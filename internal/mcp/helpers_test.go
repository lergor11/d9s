package mcp

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
)

// fakeDriver answers with whatever the test set on it, and records what it was
// asked to execute so a refusal can be shown to have executed nothing.
type fakeDriver struct {
	mu sync.Mutex

	databases []db.Database
	tables    []db.Table
	columns   []db.Column
	result    db.Result
	listErr   error

	executed []string
	closed   bool
}

func (d *fakeDriver) Connect(context.Context, db.Target) error { return nil }

func (d *fakeDriver) ListDatabases(context.Context) ([]db.Database, error) {
	return d.databases, d.listErr
}

func (d *fakeDriver) ListTables(context.Context) ([]db.Table, error) { return d.tables, d.listErr }

func (d *fakeDriver) ListColumns(context.Context, string) ([]db.Column, error) {
	return d.columns, d.listErr
}

func (d *fakeDriver) Execute(_ context.Context, statement string) db.Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.executed = append(d.executed, statement)
	res := d.result
	res.Statement = statement
	return res
}

func (d *fakeDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// ran returns the statements the driver was asked to execute.
func (d *fakeDriver) ran() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.executed...)
}

// isClosed reports whether the driver was shut down.
func (d *fakeDriver) isClosed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// fakeOpener hands out one fakeDriver per connection and counts how often it
// was asked to open, which is how session reuse is observed. It implements
// connector.
type fakeOpener struct {
	mu      sync.Mutex
	drivers map[string]*fakeDriver
	opens   int
	secret  string // registered with the redactor, as a real connect would
	red     *redactor
	err     error
	closed  bool
}

func (o *fakeOpener) open(_ context.Context, conn config.Connection, database string) (*session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return nil, o.err
	}
	o.opens++
	if o.secret != "" && o.red != nil {
		o.red.add(o.secret)
	}
	key := conn.Name + "/" + database
	drv, ok := o.drivers[key]
	if !ok {
		drv = &fakeDriver{}
		o.drivers[key] = drv
	}
	return &session{driver: drv}, nil
}

func (o *fakeOpener) close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	return nil
}

// driver returns the driver the opener handed out for a connection's default
// database, creating it so a test can arm it before the first call.
func (o *fakeOpener) driver(name string) *fakeDriver {
	o.mu.Lock()
	defer o.mu.Unlock()
	key := name + "/"
	drv, ok := o.drivers[key]
	if !ok {
		drv = &fakeDriver{}
		o.drivers[key] = drv
	}
	return drv
}

func (o *fakeOpener) openCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opens
}

func newFakeOpener() *fakeOpener {
	return &fakeOpener{drivers: map[string]*fakeDriver{}}
}

// testConfig is the fixture the tool tests run against: a production
// connection an allowlist can hide, a staging one that opts in to writes, and
// one whose password is a literal that must never be echoed.
func testConfig() *config.Config {
	return &config.Config{Connections: []config.Connection{
		{Name: "prod-pg", Type: config.Postgres, Host: "10.0.0.1", Port: 5432, User: "app",
			Password: "op://Infra/prod-pg/password", Database: "app"},
		{Name: "staging-pg", Type: config.Postgres, Host: "10.0.0.2", Port: 5432, User: "app",
			Password: "${STAGING_PASSWORD}", AllowWrite: true},
		{Name: "legacy-ch", Type: config.ClickHouse, Host: "ch.internal", Port: 9000, User: "default",
			Password: "hunter2-in-the-config"},
	}}
}

// newTestServer builds a server backed by the fake opener rather than a real
// engine, and returns both so a test can inspect what the driver saw.
func newTestServer(t *testing.T, opts Options) (*Server, *fakeOpener) {
	t.Helper()
	opener := newFakeOpener()
	if opts.Config == nil {
		opts.Config = testConfig()
	}
	opts.connector = opener
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opener.red = srv.red
	return srv, opener
}

// connectTest starts the server on an in-memory transport and returns a
// connected client session, so tests exercise the real handler path — schema
// validation, redaction and the byte cap included.
func connectTest(t *testing.T, srv *Server) *mcpsdk.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.serve(ctx, serverTransport) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connecting to the server: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		cancel()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop within 5s")
		}
	})
	return cs
}

// callTool calls one tool and returns its text and whether it reported an
// error. A protocol-level failure fails the test: a refusal must reach the
// agent as a tool error it can read, not as a broken call.
func callTool(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String(), res.IsError
}

// errFake stands in for an engine failure.
var errFake = errors.New("fake engine failure")
