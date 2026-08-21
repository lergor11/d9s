//go:build integration

package ui

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// Completion against a live postgres catalog. Start the engine the way the
// driver integration tests do:
//
//	docker run -d --rm -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
//	D9S_IT_PG_PORT=15432 D9S_IT_PG_PASSWORD=secret go test -tags integration ./internal/ui
//
// The test skips unless D9S_IT_PG_PORT is set.
func TestCompletionAgainstLivePostgres(t *testing.T) {
	raw := os.Getenv("D9S_IT_PG_PORT")
	if raw == "" {
		t.Skip("D9S_IT_PG_PORT not set; skipping live-engine test")
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("D9S_IT_PG_PORT=%q is not a port number: %v", raw, err)
	}

	drv, err := db.New(config.Postgres)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target := db.Target{
		Config: config.Connection{
			Name: "it-pg", Type: config.Postgres, Host: "127.0.0.1", Port: port,
			User: "postgres", Database: "postgres",
			TLS: &config.TLS{Mode: config.TLSDisable},
		},
		Password: os.Getenv("D9S_IT_PG_PASSWORD"),
	}
	if err := drv.Connect(ctx, target); err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })

	const create = `CREATE TABLE IF NOT EXISTS d9s_completion (
		id bigint NOT NULL, email text, created_at timestamptz)`
	if res := drv.Execute(ctx, create); res.Err != nil {
		t.Fatalf("CREATE TABLE: %v", res.Err)
	}
	t.Cleanup(func() {
		_ = drv.Execute(context.Background(), "DROP TABLE IF EXISTS d9s_completion")
	})

	m := newCompletionModel(t, config.Postgres, drv)

	// A table name the catalog knows, completed from a prefix.
	setEditor(&m.query, "SELECT * FROM d9s_comp")
	press(m, "tab")
	if got, want := m.query.ta.Value(), "SELECT * FROM d9s_completion"; got != want {
		t.Errorf("editor = %q, want %q", got, want)
	}

	// Its columns, in a position that asks for them.
	setEditor(&m.query, "SELECT | FROM d9s_completion")
	press(m, "tab")
	if !m.query.comp.open {
		t.Fatalf("no popup of columns; editor = %q status = %q", m.query.ta.Value(), m.status)
	}
	if want := []string{"created_at", "email", "id"}; !reflect.DeepEqual(m.query.comp.items, want) {
		t.Errorf("columns = %#v, want %#v", m.query.comp.items, want)
	}

	// A table created after the cache filled shows up once it is refreshed.
	press(m, "esc")
	if res := drv.Execute(ctx, "CREATE TABLE IF NOT EXISTS d9s_completion_two (id int)"); res.Err != nil {
		t.Fatalf("CREATE TABLE: %v", res.Err)
	}
	t.Cleanup(func() {
		_ = drv.Execute(context.Background(), "DROP TABLE IF EXISTS d9s_completion_two")
	})
	press(m, "ctrl+g")
	setEditor(&m.query, "SELECT * FROM d9s_completion_t")
	press(m, "tab")
	if got, want := m.query.ta.Value(), "SELECT * FROM d9s_completion_two"; got != want {
		t.Errorf("editor = %q, want the refreshed cache to offer %q", got, want)
	}
}
