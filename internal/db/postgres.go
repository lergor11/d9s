package db

import (
	"context"
	sqldriver "database/sql/driver"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lergor11/d9s/internal/config"
)

func init() {
	Register(config.Postgres, func() Driver { return &postgresDriver{} })
}

type postgresDriver struct {
	conn      *pgx.Conn
	resultCap int
}

func (d *postgresDriver) Connect(ctx context.Context, t Target) error {
	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return err
	}
	// pgx reads a host with a leading slash as a unix socket directory and
	// dials <host>/.s.PGSQL.<port> instead of opening a TCP connection.
	cfg.Host = t.Config.Host
	cfg.Port = uint16(t.Config.Port)
	cfg.ConnectTimeout = t.Config.EffectiveConnectTimeout()
	cfg.User = t.Config.User
	cfg.Password = t.Password
	cfg.Database = t.Database
	if cfg.Database == "" {
		cfg.Database = t.Config.Database
	}
	if cfg.Database == "" {
		cfg.Database = "postgres"
	}
	tlsCfg, err := tlsConfigFor(ctx, t)
	if err != nil {
		return err
	}
	// nil means plaintext to pgx; Fallbacks would otherwise retry the
	// handshake with settings the user did not ask for.
	cfg.TLSConfig = tlsCfg
	cfg.Fallbacks = nil
	if t.Dial != nil {
		cfg.DialFunc = pgconn.DialFunc(t.Dial)
	}
	ctx, cancel := connectContext(ctx, t)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return connectFailed([]string{address(t.Config)}, err)
	}
	d.conn = conn
	d.resultCap = t.Config.EffectiveResultCap()
	return nil
}

func (d *postgresDriver) ListDatabases(ctx context.Context) ([]Database, error) {
	const q = `SELECT d.datname, pg_get_userbyid(d.datdba), pg_size_pretty(pg_database_size(d.datname))
FROM pg_database d WHERE NOT d.datistemplate ORDER BY 1`
	rows, err := d.conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dbs []Database
	for rows.Next() {
		var name, owner, size string
		if err := rows.Scan(&name, &owner, &size); err != nil {
			return nil, err
		}
		dbs = append(dbs, Database{Name: name, Detail: owner + ", " + size})
	}
	return dbs, rows.Err()
}

func (d *postgresDriver) ListTables(ctx context.Context) ([]Table, error) {
	const q = `SELECT n.nspname, c.relname,
       CASE WHEN c.reltuples < 0 THEN NULL ELSE c.reltuples::bigint END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY 1, 2`
	rows, err := d.conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []Table
	for rows.Next() {
		var schema, name string
		var estimate *int64
		if err := rows.Scan(&schema, &name, &estimate); err != nil {
			return nil, err
		}
		t := Table{Name: qualify(schema, name)}
		if estimate != nil {
			t.Detail = fmt.Sprintf("~%d rows", *estimate)
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func (d *postgresDriver) ListColumns(ctx context.Context, table string) ([]Column, error) {
	schema, name := splitQualified(table, "public")
	const q = `SELECT column_name, data_type, is_nullable = 'YES', COALESCE(column_default, '')
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`
	rows, err := d.conn.Query(ctx, q, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []Column
	for rows.Next() {
		var c Column
		var def string
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def); err != nil {
			return nil, err
		}
		if def != "" {
			c.Detail = "default " + def
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (d *postgresDriver) Execute(ctx context.Context, statement string) Result {
	return executeViaCursor(ctx, d, statement)
}

// ExecuteStream runs a statement and returns a cursor over its rows, leaving
// the pgx result open until the cursor is closed or ctx is cancelled.
func (d *postgresDriver) ExecuteStream(ctx context.Context, statement string) (Cursor, error) {
	rows, err := d.conn.Query(ctx, statement)
	if err != nil {
		return nil, err
	}
	fds := rows.FieldDescriptions()
	var columns []string
	for _, fd := range fds {
		columns = append(columns, fd.Name)
	}
	src := &pgSource{rows: rows, hasColumns: len(fds) > 0}
	return newCursor(ctx, columns, d.resultCap, src), nil
}

// pgSource pages a pgx result.
type pgSource struct {
	rows       pgx.Rows
	hasColumns bool
}

func (s *pgSource) fetch(n int) ([][]string, bool, error) {
	out := make([][]string, 0, min(n, 1024))
	for len(out) < n {
		if !s.rows.Next() {
			return out, true, s.rows.Err()
		}
		vals, err := s.rows.Values()
		if err != nil {
			return out, true, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i] = stringify(v)
		}
		out = append(out, row)
	}
	return out, false, nil
}

// release closes the pgx result. Close reports nothing itself; a failure that
// ended the read has already come back from fetch through rows.Err.
func (s *pgSource) release() error {
	s.rows.Close()
	return nil
}

// affected reads the command tag, which pgx only fills in once the rows are
// closed, and which only a statement without a result set reports.
func (s *pgSource) affected() int64 {
	if s.hasColumns {
		return -1
	}
	if tag := s.rows.CommandTag(); tag.Insert() || tag.Update() || tag.Delete() {
		return tag.RowsAffected()
	}
	return -1
}

func (d *postgresDriver) Close() error {
	if d.conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.conn.Close(ctx)
}

// stringify renders one cell value for display.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339)
	case string:
		return x
	}
	if dv, ok := v.(sqldriver.Valuer); ok {
		if inner, err := dv.Value(); err == nil {
			if _, again := inner.(sqldriver.Valuer); !again {
				return stringify(inner)
			}
		}
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "NULL"
		}
		return stringify(rv.Elem().Interface())
	}
	return fmt.Sprint(v)
}
