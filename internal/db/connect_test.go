package db

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lergor11/d9s/internal/config"
)

func TestAddress(t *testing.T) {
	tests := []struct {
		name string
		conn config.Connection
		want string
	}{
		{
			name: "host and port",
			conn: config.Connection{Host: "db.internal", Port: 5432},
			want: "db.internal:5432",
		},
		{
			name: "ipv6 host is bracketed",
			conn: config.Connection{Host: "::1", Port: 6379},
			want: "[::1]:6379",
		},
		{
			name: "unix socket keeps the bare directory",
			conn: config.Connection{Host: "/var/run/postgresql", Port: 5432},
			want: "/var/run/postgresql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := address(tt.conn); got != tt.want {
				t.Errorf("address() = %q, want %q", got, tt.want)
			}
		})
	}
}

// recordingDialer captures what a driver asked to dial and refuses every
// attempt, so a connect can be inspected without a live server.
type recordingDialer struct {
	mu    sync.Mutex
	calls []string // "network addr"
}

func (r *recordingDialer) dial(_ context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, network+" "+addr)
	return nil, errors.New("refused by the test")
}

func (r *recordingDialer) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func TestPostgresUnixSocketHostDialsTheSocket(t *testing.T) {
	dir := t.TempDir()
	rec := &recordingDialer{}
	conn := config.Connection{
		Name: "socket", Type: config.Postgres, Host: dir, Port: 5432, User: "postgres",
	}
	// The socket path is what pgx builds from the host directory and the port.
	want := "unix " + filepath.Join(dir, ".s.PGSQL.5432")

	drv := &postgresDriver{}
	err := drv.Connect(context.Background(), Target{Config: conn, Dial: rec.dial})
	if err == nil {
		t.Fatal("connecting to a refused socket succeeded, want an error")
	}
	calls := rec.seen()
	if len(calls) == 0 {
		t.Fatal("no dial was attempted")
	}
	for _, call := range calls {
		if call != want {
			t.Errorf("dialled %q, want %q: a unix socket host must not become a TCP connection", call, want)
		}
	}
}

func TestConnectTimeoutBoundsTheAttempt(t *testing.T) {
	// A dialer that never returns proves the timeout comes from the config
	// rather than from the caller's context or a driver default.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-blocked:
			return nil, errors.New("test over")
		}
	}
	// An IP literal, because pgx resolves a hostname before it reaches the
	// dialer and a DNS failure would end the attempt early.
	conn := config.Connection{
		Name: "slow", Type: config.Postgres, Host: "192.0.2.1", Port: 5432,
		ConnectTimeout: 150 * time.Millisecond,
		TLS:            &config.TLS{Mode: config.TLSDisable},
	}

	drv := &postgresDriver{}
	start := time.Now()
	err := drv.Connect(context.Background(), Target{Config: conn, Dial: dial})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("connecting through a dialer that never answers succeeded, want a timeout")
	}
	if !strings.Contains(err.Error(), "192.0.2.1:5432") {
		t.Errorf("error = %q, want it to name the endpoint", err)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("connect gave up after %v, before the 150ms connect_timeout elapsed", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("connect took %v, want it bounded by the 150ms connect_timeout", elapsed)
	}
}
