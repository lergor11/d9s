// Package session opens a database session from a configured connection. It
// performs the three steps a connection always needs — resolve the password,
// bring up the SSH tunnel when the connection has one, connect the engine
// driver — so a connection behaves the same however it is reached.
package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/secrets"
	"github.com/lergor11/d9s/internal/sshtunnel"
)

// Session is a connected driver together with the SSH tunnel it was reached
// through, if any. Close releases the driver, and the tunnel only when the
// session raised it itself rather than borrowing it.
type Session struct {
	// Driver is the connected engine session. It is scoped to the database
	// Open was given.
	Driver db.Driver

	tunnel *sshtunnel.Tunnel
	owned  bool
}

// Open resolves the connection's secrets, dials its bastion when it has one,
// and connects a driver scoped to database; an empty database leaves the
// engine on its default. The caller must Close the result.
func Open(ctx context.Context, res *secrets.Resolver, conn config.Connection, database string) (*Session, error) {
	return OpenTunnel(ctx, res, conn, database, nil)
}

// OpenTunnel is Open dialing through tun, a tunnel the caller owns and shares
// between the sessions of one connection; Close leaves it open, because
// closing a tunnel is terminal for every other session dialing through it.
// With a nil tun the session raises its own tunnel when the connection needs
// one and Close releases it, which is exactly Open.
func OpenTunnel(ctx context.Context, res *secrets.Resolver, conn config.Connection, database string, tun *sshtunnel.Tunnel) (*Session, error) {
	password, err := res.Resolve(ctx, conn.Password)
	if err != nil {
		return nil, fmt.Errorf("resolving the password of %q: %w", conn.Name, err)
	}
	driver, err := db.New(conn.Type)
	if err != nil {
		return nil, err
	}
	s := &Session{Driver: driver, tunnel: tun}
	if conn.SSH != nil && s.tunnel == nil {
		s.tunnel = sshtunnel.New(*conn.SSH)
		s.owned = true
	}
	target := db.Target{Config: conn, Password: password, Database: database, Secrets: res}
	if s.tunnel != nil {
		target.Dial = s.tunnel.Dial
	}
	if err := driver.Connect(ctx, target); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the driver and, when the session raised its own tunnel, the
// tunnel. A borrowed tunnel stays open for the connection's other sessions.
func (s *Session) Close() error {
	err := s.Driver.Close()
	if s.owned && s.tunnel != nil {
		if terr := s.tunnel.Close(); err == nil {
			err = terr
		}
	}
	return err
}

// Find returns the connection named name. The error lists the configured
// names, because a typo is the usual reason a name is missing.
func Find(cfg *config.Config, name string) (config.Connection, error) {
	names := make([]string, 0, len(cfg.Connections))
	for _, conn := range cfg.Connections {
		if conn.Name == name {
			return conn, nil
		}
		names = append(names, conn.Name)
	}
	if len(names) == 0 {
		return config.Connection{}, fmt.Errorf("connection %q is not configured, and the configuration has no connections", name)
	}
	return config.Connection{}, fmt.Errorf("connection %q is not configured (configured: %s)", name, strings.Join(names, ", "))
}
