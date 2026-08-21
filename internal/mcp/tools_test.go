package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

func TestListConnections(t *testing.T) {
	tests := []struct {
		name       string
		allowlist  []string
		allowWrite bool
		want       []string
		absent     []string
	}{
		{
			name: "every connection with its reference, never its secret",
			want: []string{
				"prod-pg", "op://Infra/prod-pg/password",
				"staging-pg", "${STAGING_PASSWORD}",
				"legacy-ch", "(literal in config; withheld)",
				"read-only",
			},
			absent: []string{"hunter2-in-the-config"},
		},
		{
			name:      "the allowlist hides the rest",
			allowlist: []string{"staging-pg"},
			want:      []string{"staging-pg"},
			absent:    []string{"prod-pg", "legacy-ch"},
		},
		{
			name:       "writes are shown only where both switches are open",
			allowWrite: true,
			want:       []string{"staging-pg", "allowed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t, Options{Connections: tt.allowlist, AllowWrite: tt.allowWrite})
			text, isErr := callTool(t, connectTest(t, srv), "list_connections", nil)
			if isErr {
				t.Fatalf("list_connections reported an error: %s", text)
			}
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Errorf("list_connections output is missing %q:\n%s", want, text)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(text, absent) {
					t.Errorf("list_connections output leaked %q:\n%s", absent, text)
				}
			}
		})
	}
}

func TestAllowlistHidesConnectionsFromEveryTool(t *testing.T) {
	srv, opener := newTestServer(t, Options{Connections: []string{"staging-pg"}})
	cs := connectTest(t, srv)

	calls := []struct {
		tool string
		args map[string]any
	}{
		{tool: "list_databases", args: map[string]any{"connection": "prod-pg"}},
		{tool: "list_tables", args: map[string]any{"connection": "prod-pg"}},
		{tool: "describe_table", args: map[string]any{"connection": "prod-pg", "table": "users"}},
		{tool: "query", args: map[string]any{"connection": "prod-pg", "sql": "SELECT 1"}},
	}
	for _, call := range calls {
		t.Run(call.tool, func(t *testing.T) {
			text, isErr := callTool(t, cs, call.tool, call.args)
			if !isErr {
				t.Fatalf("%s reached the hidden connection: %s", call.tool, text)
			}
			if !strings.Contains(text, `unknown connection "prod-pg"`) {
				t.Errorf("%s should report the hidden connection as unknown, got: %s", call.tool, text)
			}
		})
	}
	if n := opener.openCount(); n != 0 {
		t.Errorf("a hidden connection was opened %d times, want 0", n)
	}
}

func TestQueryRefusesDestructiveStatements(t *testing.T) {
	tests := []struct {
		name        string
		allowWrite  bool
		connection  string
		sql         string
		wantRefused bool
		wantReasons []string
	}{
		{
			name:        "refused with neither switch",
			connection:  "prod-pg",
			sql:         "DELETE FROM users",
			wantRefused: true,
			wantReasons: []string{"without --allow-write", `connection "prod-pg" does not set allow_write`},
		},
		{
			name:        "refused when only the connection opts in",
			connection:  "staging-pg",
			sql:         "DROP TABLE events",
			wantRefused: true,
			wantReasons: []string{"without --allow-write"},
		},
		{
			name:        "refused when only the server opts in",
			allowWrite:  true,
			connection:  "prod-pg",
			sql:         "TRUNCATE users",
			wantRefused: true,
			wantReasons: []string{`connection "prod-pg" does not set allow_write`},
		},
		{
			name:       "allowed when both switches are open",
			allowWrite: true,
			connection: "staging-pg",
			sql:        "DELETE FROM users",
		},
		{
			name:       "a qualified delete is not destructive",
			connection: "prod-pg",
			sql:        "DELETE FROM users WHERE id = 1",
		},
		{
			name:        "a destructive statement hidden behind a harmless one is still caught",
			connection:  "prod-pg",
			sql:         "SELECT 1; DROP TABLE users",
			wantRefused: true,
			wantReasons: []string{"DROP TABLE users"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, opener := newTestServer(t, Options{AllowWrite: tt.allowWrite})
			opener.driver(tt.connection).result = db.Result{Affected: 1}
			text, isErr := callTool(t, connectTest(t, srv), "query",
				map[string]any{"connection": tt.connection, "sql": tt.sql})

			if !tt.wantRefused {
				if isErr {
					t.Fatalf("query was refused but should have run: %s", text)
				}
				if len(opener.driver(tt.connection).ran()) == 0 {
					t.Error("query reported success without executing anything")
				}
				return
			}
			if !isErr {
				t.Fatalf("the destructive statement was not refused: %s", text)
			}
			if ran := opener.driver(tt.connection).ran(); len(ran) != 0 {
				t.Errorf("a refused query still executed %q", ran)
			}
			// The refusal must always explain the full contract, not only the
			// half that happens to be missing.
			for _, want := range append(tt.wantReasons, "--allow-write", "allow_write: true") {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal is missing %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestQueryTruncatesLargeResults(t *testing.T) {
	const total = 1000
	rows := make([][]string, total)
	for i := range rows {
		rows[i] = []string{fmt.Sprint(i), strings.Repeat("x", 40)}
	}
	srv, opener := newTestServer(t, Options{})
	opener.driver("prod-pg").result = db.Result{Columns: []string{"id", "value"}, Rows: rows, Affected: -1}

	text, isErr := callTool(t, connectTest(t, srv), "query",
		map[string]any{"connection": "prod-pg", "sql": "SELECT * FROM events"})
	if isErr {
		t.Fatalf("query failed: %s", text)
	}
	if got := strings.Count(text, "\n"); got > MaxRows+10 {
		t.Errorf("response carries %d lines, want at most the %d-row cap plus headings", got, MaxRows)
	}
	// The total is deliberately absent: the cursor stops at the cap, so
	// counting the rest would mean reading what the cap exists to avoid.
	for _, want := range []string{"Truncated by", "200-row cap", "200 rows shown and more remain"} {
		if !strings.Contains(text, want) {
			t.Errorf("the truncation notice is missing %q:\n%s", want, tail(text))
		}
	}
	if strings.Contains(text, "999") {
		t.Error("a row past the cap was returned")
	}
}

func TestQueryCutsOversizedValues(t *testing.T) {
	huge := strings.Repeat("blob", MaxBytes) // far past both the cell and byte caps
	srv, opener := newTestServer(t, Options{})
	opener.driver("prod-pg").result = db.Result{
		Columns:  []string{"id", "payload"},
		Rows:     [][]string{{"1", huge}},
		Affected: -1,
	}

	text, isErr := callTool(t, connectTest(t, srv), "query",
		map[string]any{"connection": "prod-pg", "sql": "SELECT payload FROM blobs"})
	if isErr {
		t.Fatalf("query failed: %s", text)
	}
	if len(text) > MaxBytes {
		t.Errorf("response is %d bytes, over the %d-byte cap", len(text), MaxBytes)
	}
	if !strings.Contains(text, cutMarker) {
		t.Errorf("the cut value is not marked:\n%s", tail(text))
	}
	if !strings.Contains(text, "cell cap") {
		t.Errorf("the response does not say a value was cut:\n%s", tail(text))
	}
}

func TestQueryReportsStatementFailureAndSkipsTheRest(t *testing.T) {
	srv, opener := newTestServer(t, Options{})
	opener.driver("prod-pg").result = db.Result{Err: errFake, Affected: -1}

	text, isErr := callTool(t, connectTest(t, srv), "query",
		map[string]any{"connection": "prod-pg", "sql": "SELECT 1; SELECT 2"})
	if isErr {
		t.Fatalf("a failing statement should be reported in the response, not as a tool error: %s", text)
	}
	if !strings.Contains(text, "Failed: fake engine failure") {
		t.Errorf("the failure is not reported:\n%s", text)
	}
	if !strings.Contains(text, "Skipped") {
		t.Errorf("the statement after the failure is not marked skipped:\n%s", text)
	}
	if ran := opener.driver("prod-pg").ran(); len(ran) != 1 {
		t.Errorf("executed %q, want only the first statement", ran)
	}
}

func TestQueryRejectsAnEmptyStatement(t *testing.T) {
	srv, _ := newTestServer(t, Options{})
	text, isErr := callTool(t, connectTest(t, srv), "query",
		map[string]any{"connection": "prod-pg", "sql": "  -- just a comment\n"})
	if !isErr {
		t.Fatalf("an empty script should be refused, got: %s", text)
	}
	if !strings.Contains(text, "no executable statement") {
		t.Errorf("unhelpful refusal: %s", text)
	}
}

func TestSchemaToolsRenderDriverOutput(t *testing.T) {
	srv, opener := newTestServer(t, Options{})
	drv := opener.driver("prod-pg")
	drv.databases = []db.Database{{Name: "app", Detail: "12 MB"}}
	drv.tables = []db.Table{{Name: "users", Detail: "~1200 rows"}}
	drv.columns = []db.Column{
		{Name: "id", Type: "bigint", Detail: "primary key"},
		{Name: "email", Type: "text", Nullable: true},
	}
	cs := connectTest(t, srv)

	tests := []struct {
		tool string
		args map[string]any
		want []string
	}{
		{tool: "list_databases", args: map[string]any{"connection": "prod-pg"}, want: []string{"app", "12 MB"}},
		{tool: "list_tables", args: map[string]any{"connection": "prod-pg"}, want: []string{"users", "~1200 rows"}},
		{
			tool: "describe_table",
			args: map[string]any{"connection": "prod-pg", "table": "users"},
			want: []string{"id", "bigint", "not null", "primary key", "email", "text", "null"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("%s failed: %s", tt.tool, text)
			}
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Errorf("%s output is missing %q:\n%s", tt.tool, want, text)
				}
			}
		})
	}
}

func TestDescribeTableRejectsAnEmptyName(t *testing.T) {
	srv, _ := newTestServer(t, Options{})
	text, isErr := callTool(t, connectTest(t, srv), "describe_table",
		map[string]any{"connection": "prod-pg", "table": "   "})
	if !isErr {
		t.Fatalf("an empty table name should be refused, got: %s", text)
	}
}

func TestSecretsNeverReachTheAgent(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	srv, opener := newTestServer(t, Options{})
	opener.secret = secret
	// An engine that quotes the password back in a handshake error is exactly
	// the leak the redactor exists to stop.
	opener.driver("prod-pg").result = db.Result{
		Err:      fmt.Errorf("authentication failed for user app (password %s)", secret),
		Affected: -1,
	}
	cs := connectTest(t, srv)

	text, _ := callTool(t, cs, "query", map[string]any{"connection": "prod-pg", "sql": "SELECT 1"})
	if strings.Contains(text, secret) {
		t.Errorf("the response leaked the resolved password:\n%s", text)
	}
	if !strings.Contains(text, redactedMarker) {
		t.Errorf("the response should show the secret was withheld:\n%s", text)
	}

	// The same must hold when the failure is a tool error rather than a result.
	opener.driver("prod-pg").listErr = fmt.Errorf("connection refused using password %s", secret)
	text, isErr := callTool(t, cs, "list_tables", map[string]any{"connection": "prod-pg"})
	if !isErr {
		t.Fatalf("list_tables should have failed: %s", text)
	}
	if strings.Contains(text, secret) {
		t.Errorf("the tool error leaked the resolved password:\n%s", text)
	}
}

func TestSessionsAreOpenedOnceAndClosedOnShutdown(t *testing.T) {
	srv, opener := newTestServer(t, Options{})
	opener.driver("prod-pg").result = db.Result{Affected: -1, Columns: []string{"n"}, Rows: [][]string{{"1"}}}
	cs := connectTest(t, srv)

	for range 3 {
		if _, isErr := callTool(t, cs, "query", map[string]any{"connection": "prod-pg", "sql": "SELECT 1"}); isErr {
			t.Fatal("query failed")
		}
	}
	if _, isErr := callTool(t, cs, "list_tables", map[string]any{"connection": "prod-pg"}); isErr {
		t.Fatal("list_tables failed")
	}
	if n := opener.openCount(); n != 1 {
		t.Errorf("the connection was opened %d times, want 1: sessions are not being reused", n)
	}

	// A second connection is its own session, and a second database is too.
	if _, isErr := callTool(t, cs, "list_tables", map[string]any{"connection": "prod-pg", "database": "other"}); isErr {
		t.Fatal("list_tables on another database failed")
	}
	if n := opener.openCount(); n != 2 {
		t.Errorf("a second database opened %d sessions in total, want 2", n)
	}

	if err := srv.pool.close(); err != nil {
		t.Fatalf("closing the pool: %v", err)
	}
	if !opener.driver("prod-pg").isClosed() {
		t.Error("shutdown left the session open")
	}
}

func TestNewRejectsAnUnknownAllowlistEntry(t *testing.T) {
	_, err := New(Options{Config: testConfig(), Connections: []string{"typo-pg"}})
	if err == nil {
		t.Fatal("New accepted an allowlist naming a connection that is not configured")
	}
	if !strings.Contains(err.Error(), "typo-pg") {
		t.Errorf("the error should name the unknown connection, got: %v", err)
	}
}

func TestConnectionsAreExposedInConfigurationOrder(t *testing.T) {
	conns, err := selectConnections(testConfig().Connections, []string{"legacy-ch", "prod-pg"})
	if err != nil {
		t.Fatalf("selectConnections: %v", err)
	}
	if len(conns) != 2 || conns[0].Name != "legacy-ch" || conns[1].Name != "prod-pg" {
		t.Errorf("selectConnections returned %v, want the allowlist order", names(conns))
	}
}

func names(conns []config.Connection) []string {
	out := make([]string, len(conns))
	for i, c := range conns {
		out[i] = c.Name
	}
	return out
}

// tail returns the end of a long response, which is where the notices are.
func tail(s string) string {
	if len(s) <= 600 {
		return s
	}
	return "..." + s[len(s)-600:]
}
