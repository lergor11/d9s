package db

import (
	"reflect"
	"testing"

	"github.com/lergor11/d9s/internal/config"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		script string
		want   []string
	}{
		{
			name:   "single statement no semicolon",
			engine: config.Postgres,
			script: "SELECT 1",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "two statements trailing semicolon",
			engine: config.Postgres,
			script: "SELECT 1;\nSELECT 2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "empty statements dropped",
			engine: config.Postgres,
			script: ";;  ;\nSELECT 1;;",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "semicolon inside single-quoted string",
			engine: config.Postgres,
			script: "SELECT 'a;b'; SELECT 2",
			want:   []string{"SELECT 'a;b'", "SELECT 2"},
		},
		{
			name:   "doubled quote escape in string",
			engine: config.Postgres,
			script: "SELECT 'it''s; fine'; SELECT 2",
			want:   []string{"SELECT 'it''s; fine'", "SELECT 2"},
		},
		{
			name:   "backslash escape in string",
			engine: config.Postgres,
			script: `SELECT 'a\';b'; SELECT 2`,
			want:   []string{`SELECT 'a\';b'`, "SELECT 2"},
		},
		{
			name:   "semicolon inside double-quoted identifier",
			engine: config.Postgres,
			script: `SELECT ";" FROM t; SELECT 2`,
			want:   []string{`SELECT ";" FROM t`, "SELECT 2"},
		},
		{
			name:   "semicolon inside line comment",
			engine: config.Postgres,
			script: "SELECT 1 -- not; here\n; SELECT 2",
			want:   []string{"SELECT 1 -- not; here", "SELECT 2"},
		},
		{
			name:   "semicolon inside block comment",
			engine: config.Postgres,
			script: "SELECT /* a;b */ 1; SELECT 2",
			want:   []string{"SELECT /* a;b */ 1", "SELECT 2"},
		},
		{
			name:   "comment-only statement dropped",
			engine: config.Postgres,
			script: "-- just a comment\n; SELECT 1",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "anonymous dollar quoting",
			engine: config.Postgres,
			script: "CREATE FUNCTION f() RETURNS void AS $$ BEGIN; END; $$ LANGUAGE plpgsql; SELECT 1",
			want: []string{
				"CREATE FUNCTION f() RETURNS void AS $$ BEGIN; END; $$ LANGUAGE plpgsql",
				"SELECT 1",
			},
		},
		{
			name:   "tagged dollar quoting",
			engine: config.Postgres,
			script: "SELECT $tag$ a; $$ b $tag$; SELECT 2",
			want:   []string{"SELECT $tag$ a; $$ b $tag$", "SELECT 2"},
		},
		{
			name:   "dollar parameter is not a quote",
			engine: config.Postgres,
			script: "SELECT $1; SELECT 2",
			want:   []string{"SELECT $1", "SELECT 2"},
		},
		{
			name:   "clickhouse backtick identifier",
			engine: config.ClickHouse,
			script: "SELECT `a;b` FROM t; SELECT 2",
			want:   []string{"SELECT `a;b` FROM t", "SELECT 2"},
		},
		{
			name:   "unterminated string runs to end",
			engine: config.Postgres,
			script: "SELECT 'oops; SELECT 2",
			want:   []string{"SELECT 'oops; SELECT 2"},
		},
		{
			name:   "redis one command per line",
			engine: config.Redis,
			script: "SET k v\nGET k\n",
			want:   []string{"SET k v", "GET k"},
		},
		{
			name:   "redis skips blanks and comments",
			engine: config.Redis,
			script: "# warm the cache\n\n  GET k  \n#done\nDEL k",
			want:   []string{"GET k", "DEL k"},
		},
		{
			name:   "redis empty script",
			engine: config.Redis,
			script: "\n# nothing\n",
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.engine, tt.script)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Split(%q, %q) = %#v, want %#v", tt.engine, tt.script, got, tt.want)
			}
		})
	}
}

func TestDestructive(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		stmts  []string
		want   []string
	}{
		{
			name:   "delete without where flagged",
			engine: config.Postgres,
			stmts:  []string{"DELETE FROM users"},
			want:   []string{"DELETE FROM users"},
		},
		{
			name:   "delete with where not flagged",
			engine: config.Postgres,
			stmts:  []string{"DELETE FROM users WHERE id = 1"},
			want:   nil,
		},
		{
			name:   "update with where in subquery only is flagged",
			engine: config.Postgres,
			stmts:  []string{"UPDATE t SET a = (SELECT b FROM x WHERE y = 1)"},
			want:   []string{"UPDATE t SET a = (SELECT b FROM x WHERE y = 1)"},
		},
		{
			name:   "update with where in string only is flagged",
			engine: config.Postgres,
			stmts:  []string{"UPDATE t SET note = 'WHERE'"},
			want:   []string{"UPDATE t SET note = 'WHERE'"},
		},
		{
			name:   "update with top-level where not flagged",
			engine: config.Postgres,
			stmts:  []string{"UPDATE t SET a = 1 WHERE b = 2"},
			want:   nil,
		},
		{
			name:   "lowercase delete without where flagged",
			engine: config.Postgres,
			stmts:  []string{"delete from users"},
			want:   []string{"delete from users"},
		},
		{
			name:   "lowercase delete with where not flagged",
			engine: config.Postgres,
			stmts:  []string{"delete from users where id = 1"},
			want:   nil,
		},
		{
			name:   "drop truncate alter flagged",
			engine: config.Postgres,
			stmts:  []string{"DROP TABLE t", "TRUNCATE t", "ALTER TABLE t ADD c int", "SELECT 1"},
			want:   []string{"DROP TABLE t", "TRUNCATE t", "ALTER TABLE t ADD c int"},
		},
		{
			name:   "leading comment and whitespace skipped",
			engine: config.Postgres,
			stmts:  []string{"  /* careful */ drop table t", "-- note\n  truncate t"},
			want:   []string{"  /* careful */ drop table t", "-- note\n  truncate t"},
		},
		{
			name:   "select mentioning drop in string not flagged",
			engine: config.Postgres,
			stmts:  []string{"SELECT 'DROP TABLE t'"},
			want:   nil,
		},
		{
			name:   "clickhouse alter flagged",
			engine: config.ClickHouse,
			stmts:  []string{"ALTER TABLE t DELETE WHERE 1"},
			want:   []string{"ALTER TABLE t DELETE WHERE 1"},
		},
		{
			name:   "redis flush and del flagged",
			engine: config.Redis,
			stmts:  []string{"FLUSHALL", "flushdb", "DEL k1 k2", "GET k", "SHUTDOWN"},
			want:   []string{"FLUSHALL", "flushdb", "DEL k1 k2", "SHUTDOWN"},
		},
		{
			name:   "redis config set flagged config get not",
			engine: config.Redis,
			stmts:  []string{"CONFIG SET maxmemory 100mb", "CONFIG GET maxmemory", "config set save ''"},
			want:   []string{"CONFIG SET maxmemory 100mb", "config set save ''"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Destructive(tt.engine, tt.stmts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Destructive(%q, %#v) = %#v, want %#v", tt.engine, tt.stmts, got, tt.want)
			}
		})
	}
}
