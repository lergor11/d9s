package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/sshtunnel"
)

// session is one live engine session, bound to one database.
type session struct {
	driver db.Driver
}

// close releases the driver. The SSH tunnel, if any, outlives the session: it
// belongs to the connection, not to one database on it.
func (s *session) close() error { return s.driver.Close() }

// connector opens sessions and owns whatever they share. Tests substitute one
// backed by a fake driver rather than reaching a real engine.
type connector interface {
	// open establishes a session for one connection and database.
	open(ctx context.Context, conn config.Connection, database string) (*session, error)
	// close releases the resources shared across sessions, after the pool has
	// closed the sessions themselves.
	close() error
}

// errPoolClosed is returned once the server has begun shutting down.
var errPoolClosed = errors.New("the server is shutting down")

// pool holds the sessions the server has opened, so a second tool call on a
// connection reuses the session instead of re-resolving the 1Password secret
// and re-dialing the bastion. The key is the connection name and the database,
// because an engine binds one database at connect time: asking for a different
// one is a different session. What is expensive is shared across those
// sessions rather than duplicated — the resolver caches the secret, and the
// connector keeps one bastion connection per configured connection.
//
// One mutex covers the whole pool, including the connect itself, so two calls
// racing on the same connection cannot open two sessions. Connects are rare —
// one per connection per process — and an MCP client issues tool calls in
// sequence, so the serialization costs nothing in practice.
type pool struct {
	mu       sync.Mutex
	conn     connector
	sessions map[string]*session
	closed   bool
}

func newPool(conn connector) *pool {
	return &pool{conn: conn, sessions: map[string]*session{}}
}

// get returns the session for a connection and database, opening it on first
// use. A failed connect is not remembered: the bastion may come back.
func (p *pool) get(ctx context.Context, conn config.Connection, database string) (*session, error) {
	key := conn.Name + "\x00" + database
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errPoolClosed
	}
	if s, ok := p.sessions[key]; ok {
		return s, nil
	}
	s, err := p.conn.open(ctx, conn, database)
	if err != nil {
		return nil, err
	}
	p.sessions[key] = s
	return s, nil
}

// close releases every pooled session, then everything they shared, and
// refuses further use of the pool.
func (p *pool) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	var err error
	for key, s := range p.sessions {
		if cerr := s.close(); cerr != nil && err == nil {
			err = cerr
		}
		delete(p.sessions, key)
	}
	if cerr := p.conn.close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

// liveOpener connects for real: resolve the secret, raise the SSH tunnel when
// the connection needs one, then hand both to the engine driver. It is the
// same sequence the TUI runs when the user opens a connection.
type liveOpener struct {
	resolver *recordingResolver

	mu sync.Mutex
	// tunnels holds one bastion connection per configured connection, keyed by
	// its name, shared by every database session on it — the arrangement
	// sshtunnel.Tunnel is built for. Giving each session its own would turn
	// browsing three databases into three bastion handshakes.
	tunnels map[string]*sshtunnel.Tunnel
}

func newLiveOpener(resolver *recordingResolver) *liveOpener {
	return &liveOpener{resolver: resolver, tunnels: map[string]*sshtunnel.Tunnel{}}
}

// tunnelFor returns the connection's tunnel, creating the handle on first use.
// Creating one costs nothing: sshtunnel.New performs no I/O, and the bastion
// is dialed lazily by the first driver that needs it.
func (o *liveOpener) tunnelFor(conn config.Connection) *sshtunnel.Tunnel {
	o.mu.Lock()
	defer o.mu.Unlock()
	tunnel, ok := o.tunnels[conn.Name]
	if !ok {
		tunnel = sshtunnel.New(*conn.SSH)
		o.tunnels[conn.Name] = tunnel
	}
	return tunnel
}

// open implements connector.
func (o *liveOpener) open(ctx context.Context, conn config.Connection, database string) (*session, error) {
	password, err := o.resolver.Resolve(ctx, conn.Password)
	if err != nil {
		return nil, fmt.Errorf("resolving the password of connection %q: %w", conn.Name, err)
	}
	driver, err := db.New(conn.Type)
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", conn.Name, err)
	}
	target := db.Target{Config: conn, Password: password, Database: database, Secrets: o.resolver}
	if conn.SSH != nil {
		target.Dial = o.tunnelFor(conn).Dial
	}
	if err := driver.Connect(ctx, target); err != nil {
		// Only the driver is torn down. Closing the tunnel here would close it
		// for every other database on the connection, and permanently: a
		// closed Tunnel refuses to dial again. Left alone it reconnects by
		// itself when the next session needs it.
		_ = driver.Close()
		return nil, fmt.Errorf("connecting to %q: %w", conn.Name, err)
	}
	return &session{driver: driver}, nil
}

// close implements connector, tearing down every bastion connection.
func (o *liveOpener) close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var err error
	for name, tunnel := range o.tunnels {
		if cerr := tunnel.Close(); cerr != nil && err == nil {
			err = cerr
		}
		delete(o.tunnels, name)
	}
	return err
}
