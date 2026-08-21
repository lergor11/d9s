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
// through, if any. Close releases both.
type Session struct {
	// Driver is the connected engine session. It is scoped to the database
	// Open was given.
	Driver db.Driver

	tunnel *sshtunnel.Tunnel
}

// Open resolves the connection's secrets, dials its bastion when it has one,
// and connects a driver scoped to database; an empty database leaves the
// engine on its default. The caller must Close the result.
func Open(ctx context.Context, res *secrets.Resolver, conn config.Connection, database string) (*Session, error) {
	password, err := res.Resolve(ctx, conn.Password)
	if err != nil {
		return nil, fmt.Errorf("resolving the password of %q: %w", conn.Name, err)
	}
	driver, err := db.New(conn.Type)
	if err != nil {
		return nil, err
	}
	s := &Session{Driver: driver}
	target := db.Target{Config: conn, Password: password, Database: database, Secrets: res}
	if conn.SSH != nil {
		s.tunnel = sshtunnel.New(*conn.SSH)
		target.Dial = s.tunnel.Dial
	}
	if err := driver.Connect(ctx, target); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the driver and, when the connection used one, the tunnel.
func (s *Session) Close() error {
	err := s.Driver.Close()
	if s.tunnel != nil {
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
