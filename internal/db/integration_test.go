//go:build integration

package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lergor11/d9s/internal/config"
)

// Integration tests run against live engines started by the caller, e.g.
//
//	docker run -d --rm -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
//	docker run -d --rm -p 16379:6379 redis:7-alpine
//	docker run -d --rm -p 19000:9000 clickhouse/clickhouse-server:24-alpine
//
//	go test -tags integration ./internal/db
//
// Each test skips unless its D9S_IT_*_PORT variable is set.
//
// Those containers serve plaintext, so the targets below opt out of TLS; a
// direct connection would otherwise default to require.

// plaintext is the tls block a local development container needs.
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

// connect dials the engine, retrying until the container is ready.
func connect(t *testing.T, target Target) Driver {
	t.Helper()
	drv, err := New(target.Config.Type)
	if err != nil {
		t.Fatalf("New(%s): %v", target.Config.Type, err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = drv.Connect(ctx, target)
		cancel()
		if err == nil {
			t.Cleanup(func() { _ = drv.Close() })
			return drv
		}
		if time.Now().After(deadline) {
			t.Fatalf("connecting to %s: %v", target.Config.Type, err)
		}
		time.Sleep(time.Second)
	}
}

func TestPostgresLive(t *testing.T) {
	port := envPort(t, "D9S_IT_PG_PORT")
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-pg", Type: config.Postgres, Host: "127.0.0.1", Port: port,
		User: "postgres", Database: "postgres",
		TLS: plaintext,
	}, Password: os.Getenv("D9S_IT_PG_PASSWORD")})

	ctx := context.Background()
	dbs, err := drv.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if !containsDatabase(dbs, "postgres") {
		t.Errorf("databases = %v, want one named postgres", dbs)
	}

	res := drv.Execute(ctx, "SELECT 1 AS x, 'two' AS y, NULL AS z")
	if res.Err != nil {
		t.Fatalf("Execute: %v", res.Err)
	}
	if got, want := res.Columns, []string{"x", "y", "z"}; !equalStrings(got, want) {
		t.Errorf("columns = %v, want %v", got, want)
	}
	if len(res.Rows) != 1 || !equalStrings(res.Rows[0], []string{"1", "two", "NULL"}) {
		t.Errorf("rows = %v, want [[1 two NULL]]", res.Rows)
	}

	if res := drv.Execute(ctx, "CREATE TEMP TABLE t (id int)"); res.Err != nil {
		t.Fatalf("CREATE: %v", res.Err)
	}
	res = drv.Execute(ctx, "INSERT INTO t VALUES (1), (2)")
	if res.Err != nil {
		t.Fatalf("INSERT: %v", res.Err)
	}
	if res.Affected != 2 {
		t.Errorf("affected = %d, want 2", res.Affected)
	}
	if res := drv.Execute(ctx, "SELECT * FROM nonexistent_table"); res.Err == nil {
		t.Error("querying a missing table succeeded, want an error in the result")
	}

	if res := drv.Execute(ctx, "CREATE TABLE IF NOT EXISTS d9s_it (id int NOT NULL, note text DEFAULT 'x')"); res.Err != nil {
		t.Fatalf("CREATE TABLE: %v", res.Err)
	}
	t.Cleanup(func() { _ = drv.Execute(context.Background(), "DROP TABLE IF EXISTS d9s_it") })

	tables, err := drv.ListTables(ctx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !containsTable(tables, "d9s_it") {
		t.Errorf("tables = %v, want one named d9s_it", tables)
	}

	cols, err := drv.ListColumns(ctx, "d9s_it")
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("got %d columns, want 2: %v", len(cols), cols)
	}
	if cols[0].Name != "id" || cols[0].Nullable {
		t.Errorf("first column = %+v, want a non-nullable id", cols[0])
	}
	if cols[1].Name != "note" || !cols[1].Nullable || cols[1].Detail == "" {
		t.Errorf("second column = %+v, want a nullable note carrying its default", cols[1])
	}
}

// TestPostgresPagingLive pages a 100k-row table, which is the case that used
// to buffer the whole thing before anything appeared. It runs against the same
// container as TestPostgresLive:
//
//	D9S_IT_PG_PORT=15432 D9S_IT_PG_PASSWORD=secret \
//	  go test -tags integration -run TestPostgresPagingLive ./internal/db
func TestPostgresPagingLive(t *testing.T) {
	port := envPort(t, "D9S_IT_PG_PORT")
	const rows = 100000
	conn := config.Connection{
		Name: "it-pg-paging", Type: config.Postgres, Host: "127.0.0.1", Port: port,
		User: "postgres", Database: "postgres",
		TLS: plaintext,
	}
	drv := connect(t, Target{Config: conn, Password: os.Getenv("D9S_IT_PG_PASSWORD")})
	ctx := context.Background()

	const wide = `SELECT g AS id, repeat('x', 200) AS filler FROM generate_series(1, 100000) g`
	streamer, ok := drv.(Streamer)
	if !ok {
		t.Fatal("the postgres driver does not implement Streamer")
	}

	t.Run("the first page arrives without reading the result", func(t *testing.T) {
		start := time.Now()
		cur, err := streamer.ExecuteStream(ctx, wide)
		if err != nil {
			t.Fatalf("ExecuteStream: %v", err)
		}
		defer func() { _ = cur.Close() }()

		if types := cur.ColumnTypes(); len(types) != 2 || types[0] != "int4" || types[1] != "text" {
			t.Errorf("ColumnTypes = %v, want [int4 text]", types)
		}

		page, err := cur.NextPage(50)
		firstPage := time.Since(start)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}
		if len(page) != 50 {
			t.Fatalf("first page has %d rows, want 50", len(page))
		}
		if page[0][0] != "1" {
			t.Errorf("first row is %v, want it to start at 1", page[0])
		}
		if cur.Done() {
			t.Error("cursor is done after one page of a 100k-row result")
		}
		// Reading all 100k rows takes far longer than this; the point is that
		// the first page does not wait for them.
		if firstPage > 5*time.Second {
			t.Errorf("the first page took %v, which suggests the result was buffered", firstPage)
		}
		t.Logf("first page of 50 rows in %v", firstPage)
	})

	t.Run("the cap stops the read and reports truncation", func(t *testing.T) {
		capped := conn
		capped.ResultCap = 2500
		cur, err := connectStreamer(t, capped).ExecuteStream(ctx, wide)
		if err != nil {
			t.Fatalf("ExecuteStream: %v", err)
		}
		defer func() { _ = cur.Close() }()

		var loaded int
		for !cur.Done() {
			page, err := cur.NextPage(500)
			if err != nil {
				t.Fatalf("NextPage: %v", err)
			}
			loaded += len(page)
		}
		if loaded != capped.ResultCap {
			t.Errorf("loaded %d rows, want to stop at the cap of %d", loaded, capped.ResultCap)
		}
		if !cur.Truncated() {
			t.Error("a result stopped at the cap does not report itself truncated")
		}

		// The documented continuation: raise the cap and keep going.
		cur.SetCap(capped.ResultCap + 500)
		more, err := cur.NextPage(500)
		if err != nil {
			t.Fatalf("NextPage after raising the cap: %v", err)
		}
		if len(more) != 500 {
			t.Errorf("continuing produced %d rows, want 500 more", len(more))
		}
	})

	t.Run("closing early leaves the connection usable", func(t *testing.T) {
		// An abandoned pgx result pins its connection, so the follow-up query
		// is the real assertion here.
		cur, err := streamer.ExecuteStream(ctx, wide)
		if err != nil {
			t.Fatalf("ExecuteStream: %v", err)
		}
		if _, err := cur.NextPage(10); err != nil {
			t.Fatalf("NextPage: %v", err)
		}
		if err := cur.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		res := drv.Execute(ctx, "SELECT 42")
		if res.Err != nil {
			t.Fatalf("the connection is unusable after closing a cursor early: %v", res.Err)
		}
		if len(res.Rows) != 1 || res.Rows[0][0] != "42" {
			t.Errorf("follow-up query returned %v, want [[42]]", res.Rows)
		}
	})

	t.Run("cancelling releases the cursor", func(t *testing.T) {
		// Cancelling aborts the query itself, and pgx tears the connection
		// down with it rather than leave the protocol stream half-read. That
		// is what a cancelled Execute has always done, so the session is
		// spent either way; what matters here is that the cursor notices and
		// lets go of the result instead of holding it open. Code that only
		// wants to stop reading should Close the cursor, which keeps the
		// session, as the subtest above shows.
		own := connectStreamer(t, conn)
		cancelCtx, cancel := context.WithCancel(ctx)
		cur, err := own.ExecuteStream(cancelCtx, wide)
		if err != nil {
			t.Fatalf("ExecuteStream: %v", err)
		}
		if _, err := cur.NextPage(10); err != nil {
			t.Fatalf("NextPage: %v", err)
		}
		cancel()

		// The release runs on another goroutine; wait rather than race it.
		deadline := time.Now().Add(10 * time.Second)
		for !cur.Done() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !cur.Done() {
			t.Fatal("the cursor is still open after its context was cancelled")
		}
		if _, err := cur.NextPage(10); !errors.Is(err, context.Canceled) {
			t.Errorf("NextPage after cancellation = %v, want context.Canceled: an "+
				"abandoned read must not look like a finished one", err)
		}
		// The driver this subtest opened is spent, but the one every other
		// subtest shares was never touched.
		if res := drv.Execute(ctx, "SELECT 43"); res.Err != nil {
			t.Fatalf("an unrelated session broke: %v", res.Err)
		}
	})

	t.Run("Execute still returns every row", func(t *testing.T) {
		// The cap must not reach the one-shot path: the CLI, the MCP server
		// and export all read whole results through it.
		capped := conn
		capped.ResultCap = 100
		res := connectDriver(t, capped).Execute(ctx,
			`SELECT g FROM generate_series(1, 100000) g`)
		if res.Err != nil {
			t.Fatalf("Execute: %v", res.Err)
		}
		if len(res.Rows) != rows {
			t.Errorf("Execute returned %d rows, want all %d despite a cap of %d",
				len(res.Rows), rows, capped.ResultCap)
		}
	})
}

// connectDriver opens a second session to the same target, for a case that
// needs its own connection settings.
func connectDriver(t *testing.T, conn config.Connection) Driver {
	t.Helper()
	return connect(t, Target{Config: conn, Password: os.Getenv("D9S_IT_PG_PASSWORD")})
}

// connectStreamer is connectDriver for a driver known to page.
func connectStreamer(t *testing.T, conn config.Connection) Streamer {
	t.Helper()
	drv := connectDriver(t, conn)
	s, ok := drv.(Streamer)
	if !ok {
		t.Fatalf("the %s driver does not implement Streamer", conn.Type)
	}
	return s
}

// TestPostgresSocketLive connects over a unix socket, with no tls block, so it
// also covers the default resolving to disable there: postgres refuses TLS on
// a socket, and require-by-default would fail before the session started.
// Expose a socket with:
//
//	mkdir -p /tmp/d9s-pgsock
//	docker run -d --rm --name d9s-pgsock -e POSTGRES_HOST_AUTH_METHOD=trust \
//	  -v /tmp/d9s-pgsock:/var/run/postgresql postgres:16-alpine
//
//	D9S_IT_PG_SOCKET_DIR=/tmp/d9s-pgsock go test -tags integration -run TestPostgresSocketLive ./internal/db
func TestPostgresSocketLive(t *testing.T) {
	dir := os.Getenv("D9S_IT_PG_SOCKET_DIR")
	if dir == "" {
		t.Skip("D9S_IT_PG_SOCKET_DIR not set; skipping unix socket test")
	}
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-pg-socket", Type: config.Postgres, Host: dir, Port: 5432,
		User: "postgres", Database: "postgres",
	}})

	ctx := context.Background()
	// inet_server_addr() is NULL exactly when the client came in over a
	// socket, so this fails if the host silently became a TCP address.
	res := drv.Execute(ctx, "SELECT inet_server_addr() IS NULL")
	if res.Err != nil {
		t.Fatalf("querying inet_server_addr: %v", res.Err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "true" {
		t.Errorf("inet_server_addr() IS NULL = %v, want [[true]]: the connection is not over the socket", res.Rows)
	}

	dbs, err := drv.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if !containsDatabase(dbs, "postgres") {
		t.Errorf("databases = %v, want one named postgres", dbs)
	}
}

// TestPostgresTLSLive exercises the TLS modes against a postgres serving a
// certificate for "localhost" issued by a throwaway CA. Bring one up with:
//
//	certs=$(mktemp -d)
//	openssl req -x509 -newkey rsa:2048 -nodes -days 1 -keyout $certs/ca.key \
//	  -out $certs/ca.crt -subj /CN=d9s-test-ca
//	openssl req -newkey rsa:2048 -nodes -keyout $certs/server.key \
//	  -out $certs/server.csr -subj /CN=localhost
//	openssl x509 -req -in $certs/server.csr -CA $certs/ca.crt -CAkey $certs/ca.key \
//	  -CAcreateserial -days 1 -out $certs/server.crt \
//	  -extfile <(printf 'subjectAltName=DNS:localhost')
//	printf 'FROM postgres:16-alpine\nCOPY server.crt server.key /certs/\nRUN chown postgres /certs/* && chmod 600 /certs/server.key\n' > $certs/Dockerfile
//	docker build -t d9s-pgtls $certs
//	docker run -d --rm --name d9s-pgtls -e POSTGRES_PASSWORD=secret -p 15433:5432 \
//	  d9s-pgtls -c ssl=on -c ssl_cert_file=/certs/server.crt -c ssl_key_file=/certs/server.key
//
//	D9S_IT_PGTLS_PORT=15433 D9S_IT_PGTLS_PASSWORD=secret D9S_IT_PGTLS_CA=$certs/ca.crt \
//	  go test -tags integration -run TestPostgresTLSLive ./internal/db
func TestPostgresTLSLive(t *testing.T) {
	port := envPort(t, "D9S_IT_PGTLS_PORT")
	ca := os.Getenv("D9S_IT_PGTLS_CA")
	if ca == "" {
		t.Skip("D9S_IT_PGTLS_CA not set; skipping live TLS test")
	}
	base := config.Connection{
		Name: "it-pgtls", Type: config.Postgres, Host: "127.0.0.1", Port: port,
		User: "postgres", Database: "postgres",
	}
	password := os.Getenv("D9S_IT_PGTLS_PASSWORD")

	// The certificate names localhost, so verify-full only passes once
	// server_name says so; verify-ca ignores the name either way.
	tests := []struct {
		name    string
		tls     *config.TLS // nil exercises the default for a direct connection
		wantErr string
	}{
		{name: "no tls block"},
		{name: "require", tls: &config.TLS{Mode: config.TLSRequire}},
		{name: "verify-ca", tls: &config.TLS{Mode: config.TLSVerifyCA, CA: ca}},
		{
			name:    "verify-full rejects the ip address the certificate omits",
			tls:     &config.TLS{Mode: config.TLSVerifyFull, CA: ca},
			wantErr: "127.0.0.1",
		},
		{name: "verify-full", tls: &config.TLS{Mode: config.TLSVerifyFull, CA: ca, ServerName: "localhost"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := base
			conn.TLS = tt.tls
			target := Target{Config: conn, Password: password}
			if tt.wantErr != "" {
				drv, err := New(conn.Type)
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				err = drv.Connect(ctx, target)
				_ = drv.Close()
				if err == nil {
					t.Fatalf("connecting succeeded, want an error mentioning %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			drv := connect(t, target)
			res := drv.Execute(context.Background(),
				"SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()")
			if res.Err != nil {
				t.Fatalf("querying pg_stat_ssl: %v", res.Err)
			}
			if len(res.Rows) != 1 || res.Rows[0][0] != "true" {
				t.Errorf("pg_stat_ssl.ssl = %v, want [[true]]: the session is not encrypted", res.Rows)
			}
		})
	}
}

func TestClickHouseLive(t *testing.T) {
	port := envPort(t, "D9S_IT_CH_PORT")
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-ch", Type: config.ClickHouse, Host: "127.0.0.1", Port: port,
		User: "default", Database: "default",
		TLS: plaintext,
	}})
	checkClickHouse(t, drv, "d9s_it")
}

// liveSink collects everything the driver reports, guarded because reports
// arrive on the goroutine draining the cursor.
type liveSink struct {
	mu    sync.Mutex
	progs []Progress
	last  []ProfileEvent
	logs  []LogLine
}

// Progress appends one running-totals report.
func (s *liveSink) Progress(p Progress) { s.mu.Lock(); s.progs = append(s.progs, p); s.mu.Unlock() }

// ProfileEvents keeps the latest accumulated set.
func (s *liveSink) ProfileEvents(e []ProfileEvent) { s.mu.Lock(); s.last = e; s.mu.Unlock() }

// Log appends one server log line.
func (s *liveSink) Log(l LogLine) { s.mu.Lock(); s.logs = append(s.logs, l); s.mu.Unlock() }

// TestClickHouseProgressLive verifies that a numbers() scan reports climbing
// progress, a total, and profile events over the native protocol.
func TestClickHouseProgressLive(t *testing.T) {
	port := envPort(t, "D9S_IT_CH_PORT")
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-ch-progress", Type: config.ClickHouse, Host: "127.0.0.1", Port: port,
		User: "default", Database: "default",
		TLS: plaintext,
	}})
	ps, ok := drv.(ProgressStreamer)
	if !ok {
		t.Fatal("the clickhouse driver does not implement ProgressStreamer")
	}

	sink := &liveSink{}
	// max_block_size keeps the scan in many small blocks so several progress
	// packets arrive even on a fast machine.
	cur, err := ps.ExecuteStreamProgress(context.Background(),
		"SELECT max(number) FROM (SELECT number FROM numbers(30000000)) SETTINGS max_block_size = 65536", sink)
	if err != nil {
		t.Fatalf("ExecuteStreamProgress: %v", err)
	}
	defer func() { _ = cur.Close() }()
	for !cur.Done() {
		if _, err := cur.NextPage(0); err != nil {
			t.Fatalf("NextPage: %v", err)
		}
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.progs) < 2 {
		t.Fatalf("got %d progress reports, want them arriving as the scan runs", len(sink.progs))
	}
	final := sink.progs[len(sink.progs)-1]
	if final.Rows < 30000000 {
		t.Errorf("final rows = %d, want the whole 30M scan counted", final.Rows)
	}
	if final.Bytes == 0 {
		t.Error("final bytes = 0, want bytes read reported")
	}
	if final.TotalRows == 0 {
		t.Error("total rows = 0, want the engine's total for a numbers() scan")
	}
	for i := 1; i < len(sink.progs); i++ {
		if sink.progs[i].Rows < sink.progs[i-1].Rows {
			t.Fatalf("progress went backwards: %d then %d", sink.progs[i-1].Rows, sink.progs[i].Rows)
		}
	}
	if len(sink.last) == 0 {
		t.Error("no profile events collected, want SelectedRows and friends")
	}
}

// TestClickHouseHTTPLive runs the same checks over the HTTP interface. Bring a
// server up with:
//
//	docker run -d --rm --name d9s-ch -p 19000:9000 -p 18123:8123 clickhouse/clickhouse-server:24-alpine
//
//	D9S_IT_CH_HTTP_PORT=18123 go test -tags integration -run TestClickHouseHTTPLive ./internal/db
func TestClickHouseHTTPLive(t *testing.T) {
	port := envPort(t, "D9S_IT_CH_HTTP_PORT")
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-ch-http", Type: config.ClickHouse, Host: "127.0.0.1", Port: port,
		User: "default", Database: "default",
		Protocol: config.ProtocolHTTP,
		TLS:      plaintext,
	}})
	// A separate table, so the two protocol tests cannot disturb each other.
	checkClickHouse(t, drv, "d9s_it_http")
}

// checkClickHouse exercises every driver operation against a live server, so
// both transports are held to the same behaviour.
func checkClickHouse(t *testing.T, drv Driver, table string) {
	t.Helper()
	ctx := context.Background()
	dbs, err := drv.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if !containsDatabase(dbs, "system") {
		t.Errorf("databases = %v, want one named system", dbs)
	}

	res := drv.Execute(ctx, "SELECT 1 AS x, 'two' AS y")
	if res.Err != nil {
		t.Fatalf("Execute: %v", res.Err)
	}
	if len(res.Rows) != 1 || !equalStrings(res.Rows[0], []string{"1", "two"}) {
		t.Errorf("rows = %v, want [[1 two]]", res.Rows)
	}
	if res := drv.Execute(ctx, "SELECT * FROM system.nonexistent"); res.Err == nil {
		t.Error("querying a missing table succeeded, want an error in the result")
	}

	create := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id UInt64, note Nullable(String)) ENGINE = Memory", table)
	if res := drv.Execute(ctx, create); res.Err != nil {
		t.Fatalf("CREATE TABLE: %v", res.Err)
	}
	t.Cleanup(func() {
		_ = drv.Execute(context.Background(), "DROP TABLE IF EXISTS "+table)
	})

	tables, err := drv.ListTables(ctx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !containsTable(tables, table) {
		t.Errorf("tables = %v, want one named %s", tables, table)
	}

	cols, err := drv.ListColumns(ctx, table)
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("got %d columns, want 2: %v", len(cols), cols)
	}
	if cols[0].Name != "id" || cols[0].Nullable {
		t.Errorf("first column = %+v, want a non-nullable id", cols[0])
	}
	if cols[1].Name != "note" || !cols[1].Nullable {
		t.Errorf("second column = %+v, want a nullable note", cols[1])
	}
}

func TestRedisLive(t *testing.T) {
	port := envPort(t, "D9S_IT_REDIS_PORT")
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-redis", Type: config.Redis, Host: "127.0.0.1", Port: port,
		TLS: plaintext,
	}})

	ctx := context.Background()
	dbs, err := drv.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(dbs) != 16 {
		t.Errorf("got %d databases, want 16 logical indexes", len(dbs))
	}

	if res := drv.Execute(ctx, `SET d9s:it "hello world"`); res.Err != nil {
		t.Fatalf("SET: %v", res.Err)
	}
	res := drv.Execute(ctx, "GET d9s:it")
	if res.Err != nil {
		t.Fatalf("GET: %v", res.Err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "hello world" {
		t.Errorf("GET returned %v, want [[hello world]] (quoted argument kept intact)", res.Rows)
	}
	if res := drv.Execute(ctx, "GET d9s:absent"); res.Err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "(nil)" {
		t.Errorf("missing key returned rows=%v err=%v, want [[(nil)]] and no error", res.Rows, res.Err)
	}
	if res := drv.Execute(ctx, "NOTACOMMAND"); res.Err == nil {
		t.Error("unknown command succeeded, want an error in the result")
	}
	if res := drv.Execute(ctx, "SET d9s:other 1"); res.Err != nil {
		t.Fatalf("SET: %v", res.Err)
	}
	t.Cleanup(func() { _ = drv.Execute(context.Background(), "DEL d9s:it d9s:other") })

	tables, err := drv.ListTables(ctx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !containsTable(tables, "d9s") {
		t.Errorf("prefixes = %v, want one named d9s", tables)
	}

	cols, err := drv.ListColumns(ctx, "d9s")
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("got %d keys under the d9s prefix, want 2: %v", len(cols), cols)
	}
	if cols[0].Name != "d9s:it" || cols[0].Type != "string" {
		t.Errorf("first key = %+v, want d9s:it of type string", cols[0])
	}

	if res := drv.Execute(ctx, "DEL d9s:it"); res.Err != nil {
		t.Fatalf("DEL: %v", res.Err)
	}
}

// TestRedisClusterLive checks that the cluster client reaches every shard.
// Three nodes sharing one container's network namespace can announce
// 127.0.0.1, which is then reachable both between them and from the host:
//
//	docker run -d --rm --name d9s-rediscluster \
//	  -p 17001:17001 -p 17002:17002 -p 17003:17003 redis:7-alpine sh -c '
//	    for p in 17001 17002 17003; do
//	      redis-server --port $p --cluster-enabled yes --daemonize yes \
//	        --cluster-config-file n$p.conf --cluster-announce-ip 127.0.0.1
//	    done
//	    sleep 2
//	    redis-cli --cluster create 127.0.0.1:17001 127.0.0.1:17002 127.0.0.1:17003 --cluster-yes
//	    sleep infinity'
//
//	D9S_IT_REDIS_CLUSTER_PORT=17001 D9S_IT_REDIS_CLUSTER_ADDRS=127.0.0.1:17002,127.0.0.1:17003 \
//	  go test -tags integration -run TestRedisClusterLive ./internal/db
func TestRedisClusterLive(t *testing.T) {
	port := envPort(t, "D9S_IT_REDIS_CLUSTER_PORT")
	var seeds []string
	if raw := os.Getenv("D9S_IT_REDIS_CLUSTER_ADDRS"); raw != "" {
		seeds = strings.Split(raw, ",")
	}
	drv := connect(t, Target{Config: config.Connection{
		Name: "it-redis-cluster", Type: config.Redis, Host: "127.0.0.1", Port: port,
		Mode: config.RedisCluster, Addresses: seeds,
		TLS: plaintext,
	}})

	ctx := context.Background()
	dbs, err := drv.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(dbs) != 1 || dbs[0].Name != "0" {
		t.Errorf("databases = %v, want only index 0: a cluster has no other logical databases", dbs)
	}

	// Keys chosen to hash to different slots, so they land on different
	// masters and only a fan-out scan can find them all.
	keys := []string{"d9s:c1", "d9s:c2", "d9s:c3", "d9s:c4", "d9s:c5", "d9s:c6"}
	for _, k := range keys {
		if res := drv.Execute(ctx, "SET "+k+" value"); res.Err != nil {
			t.Fatalf("SET %s: %v", k, res.Err)
		}
	}
	t.Cleanup(func() {
		for _, k := range keys {
			_ = drv.Execute(context.Background(), "DEL "+k)
		}
	})

	res := drv.Execute(ctx, "GET d9s:c1")
	if res.Err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "value" {
		t.Errorf("GET returned rows=%v err=%v, want [[value]]", res.Rows, res.Err)
	}

	cols, err := drv.ListColumns(ctx, "d9s")
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	found := map[string]bool{}
	for _, c := range cols {
		found[c.Name] = true
	}
	for _, k := range keys {
		if !found[k] {
			t.Errorf("key %s is missing from %v: the scan did not reach every master", k, cols)
		}
	}

	tables, err := drv.ListTables(ctx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !containsTable(tables, "d9s") {
		t.Errorf("prefixes = %v, want one named d9s", tables)
	}
}

func containsTable(tables []Table, name string) bool {
	for _, t := range tables {
		if t.Name == name {
			return true
		}
	}
	return false
}

func containsDatabase(dbs []Database, name string) bool {
	for _, d := range dbs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
