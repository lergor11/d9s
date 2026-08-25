package meta

import (
	"context"
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// fakeDriver answers the catalog calls from canned data and records what ran
// through Execute.
type fakeDriver struct {
	db.Driver
	executed string
}

// ListDatabases returns two canned databases.
func (d *fakeDriver) ListDatabases(context.Context) ([]db.Database, error) {
	return []db.Database{{Name: "app", Detail: "owner=app 12 MB"}, {Name: "postgres", Detail: ""}}, nil
}

// ListTables returns two canned tables.
func (d *fakeDriver) ListTables(context.Context) ([]db.Table, error) {
	return []db.Table{{Name: "users", Detail: "~100 rows"}, {Name: "orders", Detail: ""}}, nil
}

// ListColumns returns canned columns for the users table only.
func (d *fakeDriver) ListColumns(_ context.Context, table string) ([]db.Column, error) {
	if table != "users" {
		return nil, nil
	}
	return []db.Column{
		{Name: "id", Type: "int4", Nullable: false},
		{Name: "email", Type: "text", Nullable: true, Detail: "default ''"},
	}, nil
}

// Execute records the statement and returns one canned row.
func (d *fakeDriver) Execute(_ context.Context, statement string) db.Result {
	d.executed = statement
	return db.Result{Columns: []string{"name"}, Rows: [][]string{{"public"}}, Affected: -1}
}

// describingDriver adds the \d+ detail on top of fakeDriver.
type describingDriver struct{ fakeDriver }

// DescribeTable returns canned verbose detail.
func (d *describingDriver) DescribeTable(context.Context, string) (db.TableDetail, error) {
	return db.TableDetail{
		Size:    "58 MB",
		Comment: "application users",
		Indexes: []db.IndexDef{{Name: "users_pkey", Definition: "CREATE UNIQUE INDEX users_pkey ON users (id)"}},
	}, nil
}

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		driver   db.Driver
		engine   config.EngineType
		stmt     string
		wantCols []string
		wantCell []string // substrings that must appear somewhere in the rows
		wantErr  []string
	}{
		{
			name: "l lists databases", driver: &fakeDriver{}, engine: config.Postgres, stmt: `\l`,
			wantCols: []string{"name", "detail"}, wantCell: []string{"app", "postgres"},
		},
		{
			name: "dt lists tables", driver: &fakeDriver{}, engine: config.Postgres, stmt: `\dt`,
			wantCols: []string{"name", "detail"}, wantCell: []string{"users", "orders"},
		},
		{
			name: "bare d lists tables too", driver: &fakeDriver{}, engine: config.Postgres, stmt: `\d`,
			wantCell: []string{"users", "orders"},
		},
		{
			name: "d with a table describes it", driver: &fakeDriver{}, engine: config.Postgres, stmt: `\d users`,
			wantCols: []string{"column", "type", "nullable", "detail"},
			wantCell: []string{"id", "int4", "email", "false", "true"},
		},
		{
			name: "d+ adds indexes, size and comment", driver: &describingDriver{}, engine: config.Postgres, stmt: `\d+ users`,
			wantCell: []string{"users_pkey", "CREATE UNIQUE INDEX", "(size)", "58 MB", "(comment)", "application users"},
		},
		{
			name: "d+ without a describer stays a plain description", driver: &fakeDriver{}, engine: config.Redis, stmt: `\d+ users`,
			wantCell: []string{"id", "email"},
		},
		{
			name: "help lists commands", driver: &fakeDriver{}, engine: config.Postgres, stmt: `\?`,
			wantCols: []string{"command", "description"}, wantCell: []string{`\dt`, `\q`},
		},
		{
			name: "dn on postgres goes to the catalog", driver: &fakeDriver{}, engine: config.Postgres, stmt: `\dn`,
			wantCell: []string{"public"},
		},
		{
			name: "du on redis names the engine", driver: &fakeDriver{}, engine: config.Redis, stmt: `\du`,
			wantErr: []string{"redis", "roles"},
		},
		{
			name: "dn on clickhouse points at databases", driver: &fakeDriver{}, engine: config.ClickHouse, stmt: `\dn`,
			wantErr: []string{"clickhouse", `\l`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := Parse(tt.stmt)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.stmt, err)
			}
			res := Run(context.Background(), tt.driver, tt.engine, cmd, tt.stmt)
			if len(tt.wantErr) > 0 {
				if res.Err == nil {
					t.Fatalf("Run(%q) succeeded, want an error", tt.stmt)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(res.Err.Error(), want) {
						t.Errorf("error = %q, want it to mention %q", res.Err, want)
					}
				}
				return
			}
			if res.Err != nil {
				t.Fatalf("Run(%q): %v", tt.stmt, res.Err)
			}
			if res.Statement != tt.stmt {
				t.Errorf("Statement = %q, want the command %q", res.Statement, tt.stmt)
			}
			for i, want := range tt.wantCols {
				if i >= len(res.Columns) || res.Columns[i] != want {
					t.Errorf("Columns = %v, want %v", res.Columns, tt.wantCols)
					break
				}
			}
			flat := new(strings.Builder)
			for _, row := range res.Rows {
				flat.WriteString(strings.Join(row, " | ") + "\n")
			}
			for _, want := range tt.wantCell {
				if !strings.Contains(flat.String(), want) {
					t.Errorf("rows do not mention %q:\n%s", want, flat.String())
				}
			}
		})
	}
}

func TestCatalogStatementIsTheCommand(t *testing.T) {
	drv := &fakeDriver{}
	cmd, _ := Parse(`\dn`)
	res := Run(context.Background(), drv, config.Postgres, cmd, `\dn`)
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Statement != `\dn` {
		t.Errorf("Statement = %q, want the command, not the catalog SQL", res.Statement)
	}
	if !strings.Contains(drv.executed, "pg_namespace") {
		t.Errorf("executed %q, want the pg_namespace catalog query", drv.executed)
	}
}
