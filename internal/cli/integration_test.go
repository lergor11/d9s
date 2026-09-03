//go:build integration

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Integration tests drive the subcommands against live engines started by the
// caller, e.g.
//
//	docker run -d --rm --name d9s-pg -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
//	docker run -d --rm --name d9s-redis -p 16379:6379 redis:7-alpine
//	docker run -d --rm --name d9s-ch -p 19000:9000 clickhouse/clickhouse-server:24-alpine
//
//	D9S_IT_PG_PORT=15432 D9S_IT_PG_PASSWORD=secret D9S_IT_REDIS_PORT=16379 \
//	  D9S_IT_CH_PORT=19000 go test -tags integration ./internal/cli
//
// Each test skips unless its D9S_IT_*_PORT variable is set. Those containers
// serve plaintext, so the generated configurations opt out of TLS; a direct
// connection would otherwise default to require.

// liveConfig writes a configuration naming one live engine and returns its
// path, skipping the test when the engine's port variable is unset.
func liveConfig(t *testing.T, portVar, body string) string {
	t.Helper()
	raw := os.Getenv(portVar)
	if raw == "" {
		t.Skipf("%s not set; skipping live-engine test", portVar)
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not a port number: %v", portVar, raw, err)
	}
	return writeConfig(t, fmt.Sprintf(body, port))
}

// mustRun runs a subcommand and fails the test unless it exits 0.
func mustRun(t *testing.T, cfg, name string, args ...string) result {
	t.Helper()
	got := invoke(name, append([]string{"-config", cfg}, args...), env{})
	if got.code != ExitOK {
		t.Fatalf("d9s %s %v exited %d, want 0\nstderr: %s", name, args, got.code, got.stderr)
	}
	return got
}

func TestPostgresCommandsLive(t *testing.T) {
	cfg := liveConfig(t, "D9S_IT_PG_PORT", `connections:
  - name: it-pg
    type: postgres
    host: 127.0.0.1
    port: %d
    user: postgres
    database: postgres
    password: ${D9S_IT_PG_PASSWORD}
    connect_timeout: 20s
    tls:
      mode: disable
`)
	// The configuration reads the password from the environment, so the
	// variable has to exist even when the engine was started without one.
	if _, ok := os.LookupEnv("D9S_IT_PG_PASSWORD"); !ok {
		t.Setenv("D9S_IT_PG_PASSWORD", "")
	}

	// A table of this run's own, so a leftover from a previous run cannot make
	// the assertions pass on their own.
	const table = "d9s_cli_it"
	mustRun(t, cfg, "query", "it-pg", "DROP TABLE IF EXISTS "+table, "--write")
	mustRun(t, cfg, "query", "it-pg",
		fmt.Sprintf("CREATE TABLE %s (id int NOT NULL, note text DEFAULT 'x')", table))
	t.Cleanup(func() {
		invoke("query", []string{"-config", cfg, "it-pg", "DROP TABLE IF EXISTS " + table, "--write"}, env{})
	})

	t.Run("connections lists without connecting", func(t *testing.T) {
		got := mustRun(t, cfg, "connections")
		if !strings.Contains(got.stdout, `"name":"it-pg"`) {
			t.Errorf("stdout = %q, want the connection", got.stdout)
		}
	})

	t.Run("databases", func(t *testing.T) {
		got := mustRun(t, cfg, "databases", "it-pg")
		if !strings.Contains(got.stdout, `"name":"postgres"`) {
			t.Errorf("stdout = %q, want a database named postgres", got.stdout)
		}
	})

	t.Run("tables", func(t *testing.T) {
		got := mustRun(t, cfg, "tables", "it-pg")
		if !strings.Contains(got.stdout, `"name":"`+table+`"`) {
			t.Errorf("stdout = %q, want the table this test created", got.stdout)
		}
	})

	t.Run("tables of a named database", func(t *testing.T) {
		got := mustRun(t, cfg, "tables", "it-pg", "postgres")
		if !strings.Contains(got.stdout, `"name":"`+table+`"`) {
			t.Errorf("stdout = %q, want the table this test created", got.stdout)
		}
	})

	t.Run("describe", func(t *testing.T) {
		got := mustRun(t, cfg, "describe", "it-pg", table)
		lines := jsonLines(t, got.stdout)
		if len(lines) != 2 {
			t.Fatalf("got %d columns, want 2:\n%s", len(lines), got.stdout)
		}
		if lines[0]["name"] != "id" || lines[0]["nullable"] != "false" {
			t.Errorf("first column = %v, want a non-nullable id", lines[0])
		}
		if lines[1]["name"] != "note" || lines[1]["nullable"] != "true" {
			t.Errorf("second column = %v, want a nullable note", lines[1])
		}
	})

	t.Run("query plans", func(t *testing.T) {
		static := mustRun(t, cfg, "plan", "it-pg", "SELECT * FROM "+table)
		if !strings.Contains(static.stdout, "QUERY PLAN") {
			t.Errorf("static plan = %q, want PostgreSQL plan rows", static.stdout)
		}
		runtime := mustRun(t, cfg, "plan", "it-pg", "SELECT count(*) FROM "+table, "-mode", "analyze")
		if !strings.Contains(runtime.stdout, "Execution Time") || !strings.Contains(runtime.stdout, "Buffers:") {
			t.Errorf("runtime plan = %q, want timing and buffer details", runtime.stdout)
		}
	})

	t.Run("query in every format", func(t *testing.T) {
		const sql = "SELECT 1 AS x, 'two' AS y, NULL AS z"
		tests := []struct {
			format   string
			terminal bool
			want     string
		}{
			{format: "", want: `{"x":"1","y":"two","z":null}`},
			{format: "jsonl", want: `{"x":"1","y":"two","z":null}`},
			{format: "csv", want: "x,y,z\n1,two,NULL\n"},
			{format: "table", want: "x | y   | z\n"},
			{format: "", terminal: true, want: "x | y   | z\n"},
		}
		for _, tt := range tests {
			name := tt.format
			if name == "" {
				name = "default"
				if tt.terminal {
					name = "default on a terminal"
				}
			}
			t.Run(name, func(t *testing.T) {
				args := []string{"-config", cfg, "it-pg", sql}
				if tt.format != "" {
					args = append(args, "-o", tt.format)
				}
				got := invoke("query", args, env{terminal: tt.terminal})
				if got.code != ExitOK {
					t.Fatalf("exit code = %d, want 0 (stderr: %s)", got.code, got.stderr)
				}
				if !strings.Contains(got.stdout, tt.want) {
					t.Errorf("stdout =\n%s\nwant it to contain %q", got.stdout, tt.want)
				}
			})
		}
	})

	t.Run("several statements run in order", func(t *testing.T) {
		got := mustRun(t, cfg, "query", "it-pg", "SELECT 1 AS a; SELECT 2 AS b; SELECT 3 AS c")
		lines := jsonLines(t, got.stdout)
		if len(lines) != 3 {
			t.Fatalf("got %d rows, want one per statement:\n%s", len(lines), got.stdout)
		}
		for i, key := range []string{"a", "b", "c"} {
			if _, ok := lines[i][key]; !ok {
				t.Errorf("row %d = %v, want the column %q of statement %d", i+1, lines[i], key, i+1)
			}
		}
		for i := 1; i <= 3; i++ {
			if label := fmt.Sprintf("[%d] SELECT", i); !strings.Contains(got.stderr, label) {
				t.Errorf("stderr is missing the label %q:\n%s", label, got.stderr)
			}
		}
	})

	t.Run("a failing statement exits with the statement code", func(t *testing.T) {
		got := invoke("query", []string{"-config", cfg, "it-pg", "SELECT * FROM does_not_exist"}, env{})
		if got.code != ExitQuery {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitQuery, got.stderr)
		}
		if got.stdout != "" {
			t.Errorf("stdout = %q, want nothing", got.stdout)
		}
		if !strings.Contains(got.stderr, "does_not_exist") {
			t.Errorf("stderr = %q, want the engine's message", got.stderr)
		}
	})

	t.Run("a script stops at the first failure", func(t *testing.T) {
		got := invoke("query", []string{"-config", cfg, "it-pg",
			"SELECT 1 AS ran; SELECT * FROM does_not_exist; SELECT 2 AS skipped"}, env{})
		if got.code != ExitQuery {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitQuery, got.stderr)
		}
		if lines := jsonLines(t, got.stdout); len(lines) != 1 {
			t.Errorf("got %d rows, want only the statement that ran:\n%s", len(lines), got.stdout)
		}
		if !strings.Contains(got.stderr, "not run: SELECT 2 AS skipped") {
			t.Errorf("stderr = %q, want the third statement reported as not run", got.stderr)
		}
	})

	t.Run("a refused statement leaves the table alone", func(t *testing.T) {
		got := invoke("query", []string{"-config", cfg, "it-pg", "DROP TABLE " + table}, env{})
		if got.code != ExitRefused {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitRefused, got.stderr)
		}
		if !strings.Contains(got.stderr, "--write") {
			t.Errorf("stderr = %q, want it to name --write", got.stderr)
		}
		still := mustRun(t, cfg, "tables", "it-pg")
		if !strings.Contains(still.stdout, `"name":"`+table+`"`) {
			t.Fatalf("the table is gone; a refused statement must not run:\n%s", still.stdout)
		}
	})

	t.Run("a write reports the rows it changed", func(t *testing.T) {
		got := invoke("query", []string{"-config", cfg, "it-pg",
			fmt.Sprintf("INSERT INTO %s (id) VALUES (1), (2)", table), "-o", "table"}, env{})
		if got.code != ExitOK {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", got.code, got.stderr)
		}
		if !strings.Contains(got.stdout, "2 row(s) affected") {
			t.Errorf("stdout = %q, want the affected count", got.stdout)
		}
		count := mustRun(t, cfg, "query", "it-pg", "SELECT count(*) AS n FROM "+table)
		if !strings.Contains(count.stdout, `"n":"2"`) {
			t.Errorf("stdout = %q, want two rows in the table", count.stdout)
		}
	})

	t.Run("--write lets a destructive statement through", func(t *testing.T) {
		got := invoke("query", []string{"-config", cfg, "it-pg",
			"DELETE FROM " + table, "--write"}, env{})
		if got.code != ExitOK {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", got.code, got.stderr)
		}
		count := mustRun(t, cfg, "query", "it-pg", "SELECT count(*) AS n FROM "+table)
		if !strings.Contains(count.stdout, `"n":"0"`) {
			t.Errorf("stdout = %q, want the table emptied", count.stdout)
		}
	})

	t.Run("sql from stdin", func(t *testing.T) {
		got := invoke("query", []string{"-config", cfg, "it-pg"},
			env{stdin: strings.NewReader("SELECT 'piped' AS src\n")})
		if got.code != ExitOK {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", got.code, got.stderr)
		}
		if !strings.Contains(got.stdout, `"src":"piped"`) {
			t.Errorf("stdout = %q, want the piped statement's row", got.stdout)
		}
	})

	t.Run("sql from a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "script.sql")
		if err := os.WriteFile(path, []byte("SELECT 'from-a-file' AS src;\n"), 0o600); err != nil {
			t.Fatalf("writing the script: %v", err)
		}
		got := mustRun(t, cfg, "query", "it-pg", "-f", path)
		if !strings.Contains(got.stdout, `"src":"from-a-file"`) {
			t.Errorf("stdout = %q, want the file's row", got.stdout)
		}
	})

	t.Run("a connection failure differs from a statement failure", func(t *testing.T) {
		unreachable := writeConfig(t, `connections:
  - name: gone
    type: postgres
    host: 127.0.0.1
    port: 1
    user: postgres
    connect_timeout: 5s
    tls:
      mode: disable
`)
		got := invoke("query", []string{"-config", unreachable, "gone", "SELECT 1"}, env{})
		if got.code != ExitConnect {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitConnect, got.stderr)
		}
		if got.code == ExitQuery {
			t.Error("an unreachable database must not report a statement failure")
		}
	})
}

func TestClickHouseCommandsLive(t *testing.T) {
	cfg := liveConfig(t, "D9S_IT_CH_PORT", `connections:
  - name: it-ch
    type: clickhouse
    host: 127.0.0.1
    port: %d
    user: default
    database: default
    connect_timeout: 20s
    tls:
      mode: disable
`)
	const table = "d9s_cli_it_ch"
	mustRun(t, cfg, "query", "it-ch", "DROP TABLE IF EXISTS "+table, "--write")
	mustRun(t, cfg, "query", "it-ch",
		fmt.Sprintf("CREATE TABLE %s (id UInt64, note Nullable(String)) ENGINE = MergeTree ORDER BY id", table))
	mustRun(t, cfg, "query", "it-ch", fmt.Sprintf("INSERT INTO %s VALUES (1, 'one')", table))
	t.Cleanup(func() {
		invoke("query", []string{"-config", cfg, "it-ch", "DROP TABLE IF EXISTS " + table, "--write"}, env{})
	})

	if got := mustRun(t, cfg, "databases", "it-ch"); !strings.Contains(got.stdout, `"name":"system"`) {
		t.Errorf("databases = %q, want one named system", got.stdout)
	}
	if got := mustRun(t, cfg, "tables", "it-ch"); !strings.Contains(got.stdout, `"name":"`+table+`"`) {
		t.Errorf("tables = %q, want the table this test created", got.stdout)
	}
	if got := mustRun(t, cfg, "describe", "it-ch", table); !strings.Contains(got.stdout, `"name":"id"`) {
		t.Errorf("describe = %q, want the id column", got.stdout)
	}
	if got := mustRun(t, cfg, "query", "it-ch", "SELECT 1 AS x"); !strings.Contains(got.stdout, `"x":"1"`) {
		t.Errorf("query = %q, want one row", got.stdout)
	}
	for _, mode := range []string{"plan", "pipeline", "estimate"} {
		t.Run("plan "+mode, func(t *testing.T) {
			got := mustRun(t, cfg, "plan", "it-ch", "SELECT * FROM "+table, "-mode", mode)
			if strings.TrimSpace(got.stdout) == "" {
				t.Errorf("%s plan returned no output", mode)
			}
		})
	}

	got := invoke("query", []string{"-config", cfg, "it-ch", "SELECT * FROM system.does_not_exist"}, env{})
	if got.code != ExitQuery {
		t.Errorf("exit code = %d, want %d (stderr: %s)", got.code, ExitQuery, got.stderr)
	}
	got = invoke("query", []string{"-config", cfg, "it-ch", "TRUNCATE TABLE " + table}, env{})
	if got.code != ExitRefused {
		t.Errorf("exit code = %d, want %d: TRUNCATE needs --write", got.code, ExitRefused)
	}
}

func TestRedisCommandsLive(t *testing.T) {
	cfg := liveConfig(t, "D9S_IT_REDIS_PORT", `connections:
  - name: it-redis
    type: redis
    host: 127.0.0.1
    port: %d
    connect_timeout: 20s
    tls:
      mode: disable
`)
	t.Cleanup(func() {
		invoke("query", []string{"-config", cfg, "it-redis", "DEL d9s:cli", "--write"}, env{})
	})

	if got := mustRun(t, cfg, "databases", "it-redis"); len(jsonLines(t, got.stdout)) != 16 {
		t.Errorf("databases = %q, want the sixteen logical indexes", got.stdout)
	}
	mustRun(t, cfg, "query", "it-redis", `SET d9s:cli "hello world"`)
	if got := mustRun(t, cfg, "query", "it-redis", "GET d9s:cli"); !strings.Contains(got.stdout, "hello world") {
		t.Errorf("GET = %q, want the value that was set", got.stdout)
	}
	if got := mustRun(t, cfg, "tables", "it-redis"); !strings.Contains(got.stdout, `"name":"d9s"`) {
		t.Errorf("prefixes = %q, want the d9s prefix", got.stdout)
	}
	if got := mustRun(t, cfg, "describe", "it-redis", "d9s"); !strings.Contains(got.stdout, `"name":"d9s:cli"`) {
		t.Errorf("keys = %q, want the key under the prefix", got.stdout)
	}

	// One command per line, so a script is split the same way the interface
	// splits it.
	got := mustRun(t, cfg, "query", "it-redis", "SET d9s:cli one\nGET d9s:cli")
	if !strings.Contains(got.stdout, "one") {
		t.Errorf("stdout = %q, want the second command's reply", got.stdout)
	}

	got = invoke("query", []string{"-config", cfg, "it-redis", "FLUSHALL"}, env{})
	if got.code != ExitRefused {
		t.Fatalf("exit code = %d, want %d: FLUSHALL needs --write", got.code, ExitRefused)
	}
	if still := mustRun(t, cfg, "query", "it-redis", "GET d9s:cli"); strings.Contains(still.stdout, "(nil)") {
		t.Error("the key is gone; a refused FLUSHALL must not run")
	}

	got = invoke("query", []string{"-config", cfg, "it-redis", "NOTACOMMAND"}, env{})
	if got.code != ExitQuery {
		t.Errorf("exit code = %d, want %d for an unknown command", got.code, ExitQuery)
	}
}

// jsonLines decodes JSONL output into one map per line.
func jsonLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %q is not a JSON object: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}
