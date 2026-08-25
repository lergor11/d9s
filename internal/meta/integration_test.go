//go:build integration

package meta

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// Integration tests run against live engines started by the caller, the same
// containers the db package's tests use:
//
//	docker run -d --rm -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
//	docker run -d --rm -p 19000:9000 clickhouse/clickhouse-server:24-alpine
//	docker run -d --rm -p 16379:6379 redis:7-alpine
//
//	D9S_IT_PG_PORT=15432 D9S_IT_PG_PASSWORD=secret D9S_IT_CH_PORT=19000 \
//	D9S_IT_REDIS_PORT=16379 go test -tags integration ./internal/meta
//
// Each test skips unless its D9S_IT_*_PORT variable is set.

var plaintext = &config.TLS{Mode: config.TLSDisable}

func envPort(t *testing.T, name string) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		t.Skipf("%s not set; skipping live-engine test", name)
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not a port number: %v", name, raw, err)
	}
	return port
}

func connect(t *testing.T, conn config.Connection, password string) db.Driver {
	t.Helper()
	drv, err := db.New(conn.Type)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	if err := drv.Connect(context.Background(), db.Target{Config: conn, Password: password}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	return drv
}

// run parses and answers one command, failing the test on a parse error.
func run(t *testing.T, drv db.Driver, engine config.EngineType, stmt string) db.Result {
	t.Helper()
	cmd, err := Parse(stmt)
	if err != nil {
		t.Fatalf("Parse(%q): %v", stmt, err)
	}
	return Run(context.Background(), drv, engine, cmd, stmt)
}

// flatten joins the rows for substring assertions.
func flatten(res db.Result) string {
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString(strings.Join(row, " | ") + "\n")
	}
	return b.String()
}

func TestPostgresMetaLive(t *testing.T) {
	port := envPort(t, "D9S_IT_PG_PORT")
	drv := connect(t, config.Connection{
		Name: "it-pg-meta", Type: config.Postgres, Host: "127.0.0.1", Port: port,
		User: "postgres", Database: "postgres", TLS: plaintext,
	}, os.Getenv("D9S_IT_PG_PASSWORD"))
	ctx := context.Background()

	drv.Execute(ctx, "DROP TABLE IF EXISTS meta_users")
	if res := drv.Execute(ctx, `CREATE TABLE meta_users (id int PRIMARY KEY, email text)`); res.Err != nil {
		t.Fatalf("CREATE TABLE: %v", res.Err)
	}
	defer drv.Execute(ctx, "DROP TABLE meta_users")
	drv.Execute(ctx, `CREATE INDEX meta_users_email ON meta_users (email)`)
	drv.Execute(ctx, `COMMENT ON TABLE meta_users IS 'meta test table'`)

	if got := flatten(run(t, drv, config.Postgres, `\dt`)); !strings.Contains(got, "meta_users") {
		t.Errorf(`\dt does not list meta_users:%s`, got)
	}
	if got := flatten(run(t, drv, config.Postgres, `\d meta_users`)); !strings.Contains(got, "email") {
		t.Errorf(`\d meta_users does not list the email column:%s`, got)
	}
	verbose := flatten(run(t, drv, config.Postgres, `\d+ meta_users`))
	for _, want := range []string{"meta_users_email", "(size)", "(comment)", "meta test table"} {
		if !strings.Contains(verbose, want) {
			t.Errorf(`\d+ meta_users misses %q:%s`, want, verbose)
		}
	}
	if got := flatten(run(t, drv, config.Postgres, `\dn`)); !strings.Contains(got, "public") {
		t.Errorf(`\dn does not list the public schema:%s`, got)
	}
	if got := flatten(run(t, drv, config.Postgres, `\du`)); !strings.Contains(got, "postgres") {
		t.Errorf(`\du does not list the postgres role:%s`, got)
	}
}

func TestClickHouseMetaLive(t *testing.T) {
	port := envPort(t, "D9S_IT_CH_PORT")
	drv := connect(t, config.Connection{
		Name: "it-ch-meta", Type: config.ClickHouse, Host: "127.0.0.1", Port: port,
		User: "default", Database: "default", TLS: plaintext,
	}, "")
	ctx := context.Background()

	drv.Execute(ctx, "DROP TABLE IF EXISTS meta_events")
	if res := drv.Execute(ctx,
		`CREATE TABLE meta_events (ts DateTime, kind String) ENGINE = MergeTree ORDER BY ts COMMENT 'meta test table'`); res.Err != nil {
		t.Fatalf("CREATE TABLE: %v", res.Err)
	}
	defer drv.Execute(ctx, "DROP TABLE meta_events")

	if got := flatten(run(t, drv, config.ClickHouse, `\d meta_events`)); !strings.Contains(got, "kind") {
		t.Errorf(`\d meta_events does not list the kind column:%s`, got)
	}
	verbose := flatten(run(t, drv, config.ClickHouse, `\d+ meta_events`))
	for _, want := range []string{"PRIMARY KEY", "(comment)", "meta test table"} {
		if !strings.Contains(verbose, want) {
			t.Errorf(`\d+ meta_events misses %q:%s`, want, verbose)
		}
	}
	res := run(t, drv, config.ClickHouse, `\dn`)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "clickhouse") {
		t.Errorf(`\dn error = %v, want it to name clickhouse`, res.Err)
	}
}

func TestRedisMetaLive(t *testing.T) {
	port := envPort(t, "D9S_IT_REDIS_PORT")
	drv := connect(t, config.Connection{
		Name: "it-redis-meta", Type: config.Redis, Host: "127.0.0.1", Port: port,
		TLS: plaintext,
	}, "")

	if res := run(t, drv, config.Redis, `\l`); res.Err != nil || len(res.Rows) == 0 {
		t.Errorf(`\l = %v rows, err %v; want the logical databases`, len(res.Rows), res.Err)
	}
	res := run(t, drv, config.Redis, `\du`)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "redis") {
		t.Errorf(`\du error = %v, want it to name redis`, res.Err)
	}
}
