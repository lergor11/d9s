package session

import (
	"context"
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/sshtunnel"
)

// closeRecorder is a Driver that only records Close; Session.Close touches
// nothing else.
type closeRecorder struct {
	db.Driver
	closed bool
}

// Close records that the session released the driver.
func (c *closeRecorder) Close() error { c.closed = true; return nil }

func TestCloseRespectsTunnelOwnership(t *testing.T) {
	tests := []struct {
		name string
		// owned mirrors how OpenTunnel sets it: false for a caller-supplied
		// tunnel, true for one the session raised itself.
		owned      bool
		wantClosed bool
	}{
		{name: "a borrowed tunnel stays open for the connection's other sessions", owned: false, wantClosed: false},
		{name: "an owned tunnel is released", owned: true, wantClosed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tun := sshtunnel.New(config.SSH{Bastion: "bastion.invalid"})
			drv := &closeRecorder{}
			s := &Session{Driver: drv, tunnel: tun, owned: tt.owned}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if !drv.closed {
				t.Error("Close left the driver open")
			}
			// A closed tunnel refuses to dial before any network I/O; the
			// canceled context stops an open one just as early.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := tun.Dial(ctx, "tcp", "db:5432")
			gotClosed := err != nil && strings.Contains(err.Error(), "tunnel is closed")
			if gotClosed != tt.wantClosed {
				t.Errorf("tunnel closed = %v, want %v (dial error: %v)", gotClosed, tt.wantClosed, err)
			}
		})
	}
}

func TestFind(t *testing.T) {
	cfg := &config.Config{Connections: []config.Connection{
		{Name: "prod-pg", Type: config.Postgres},
		{Name: "cache", Type: config.Redis},
	}}

	tests := []struct {
		name    string
		cfg     *config.Config
		lookup  string
		wantErr []string
	}{
		{name: "the first connection", cfg: cfg, lookup: "prod-pg"},
		{name: "a later connection", cfg: cfg, lookup: "cache"},
		{
			name: "a typo names what is configured", cfg: cfg, lookup: "prod_pg",
			wantErr: []string{`"prod_pg"`, "prod-pg, cache"},
		},
		{
			name: "matching is exact", cfg: cfg, lookup: "PROD-PG",
			wantErr: []string{`"PROD-PG"`},
		},
		{
			name: "nothing configured at all", cfg: &config.Config{}, lookup: "prod-pg",
			wantErr: []string{"no connections"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := Find(tt.cfg, tt.lookup)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Find(%q): %v", tt.lookup, err)
				}
				if conn.Name != tt.lookup {
					t.Errorf("Find(%q) returned %q", tt.lookup, conn.Name)
				}
				return
			}
			if err == nil {
				t.Fatalf("Find(%q) succeeded, want an error", tt.lookup)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}
