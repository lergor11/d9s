package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
	"github.com/andreim/d9s/internal/sshtunnel"
)

// session is one live engine session plus the SSH tunnel carrying it, if any.
type session struct {
	driver db.Driver
	tunnel *sshtunnel.Tunnel
}

// close releases the driver and the tunnel, reporting the first failure while
// still closing the rest.
func (s *session) close() error {
	err := s.driver.Close()
	if s.tunnel != nil {
		if terr := s.tunnel.Close(); err == nil {
			err = terr
		}
	}
	return err
}

// openFunc establishes a session for one connection and database. Tests
// substitute a fake driver here rather than reaching a real engine.
type openFunc func(ctx context.Context, conn config.Connection, database string) (*session, error)

// errPoolClosed is returned once the server has begun shutting down.
var errPoolClosed = errors.New("the server is shutting down")

// pool holds the sessions the server has opened, so a second tool call on a
// connection reuses the session instead of re-resolving the 1Password secret
// and re-dialing the bastion. The key is the connection name and the database,
// because an engine binds one database at connect time: asking for a different
// one is a different session.
//
// One mutex covers the whole pool, including the connect itself, so two calls
// racing on the same connection cannot open two sessions. Connects are rare —
// one per connection per process — and an MCP client issues tool calls in
// sequence, so the serialization costs nothing in practice.
type pool struct {
	mu       sync.Mutex
	open     openFunc
	sessions map[string]*session
	closed   bool
}

func newPool(open openFunc) *pool {
	return &pool{open: open, sessions: map[string]*session{}}
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
	s, err := p.open(ctx, conn, database)
	if err != nil {
		return nil, err
	}
	p.sessions[key] = s
	return s, nil
}

// close releases every pooled session and refuses further use of the pool.
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
	return err
}

// liveOpener connects for real: resolve the secret, raise the SSH tunnel when
// the connection needs one, then hand both to the engine driver. It is the
// same sequence the TUI runs when the user opens a connection.
type liveOpener struct {
	resolver *recordingResolver
}

// open implements openFunc.
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
	var tunnel *sshtunnel.Tunnel
	if conn.SSH != nil {
		tunnel = sshtunnel.New(*conn.SSH)
		target.Dial = tunnel.Dial
	}
	if err := driver.Connect(ctx, target); err != nil {
		_ = driver.Close()
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return nil, fmt.Errorf("connecting to %q: %w", conn.Name, err)
	}
	return &session{driver: driver, tunnel: tunnel}, nil
}
