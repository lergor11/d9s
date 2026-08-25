package db

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/lergor11/d9s/internal/config"
)

func init() {
	Register(config.ClickHouse, func() Driver { return &clickhouseDriver{} })
}

type clickhouseDriver struct {
	conn      chdriver.Conn
	database  string
	resultCap int
}

func (d *clickhouseDriver) Connect(ctx context.Context, t Target) error {
	database := t.Database
	if database == "" {
		database = t.Config.Database
	}
	if database == "" {
		database = "default"
	}
	tlsCfg, err := tlsConfigFor(ctx, t)
	if err != nil {
		return err
	}
	addr := address(t.Config)
	protocol := clickhouse.Native
	if t.Config.EffectiveProtocol() == config.ProtocolHTTP {
		protocol = clickhouse.HTTP
	}
	opts := &clickhouse.Options{
		Addr:     []string{addr},
		Protocol: protocol,
		Auth: clickhouse.Auth{
			Database: database,
			Username: t.Config.User,
			Password: t.Password,
		},
		DialTimeout: t.Config.EffectiveConnectTimeout(),
		TLS:         tlsCfg, // nil = plaintext
	}
	if t.Dial != nil {
		dial := t.Dial
		// Over HTTP the driver layers TLS on top of DialContext itself, since
		// TLS decides the URL scheme; the native protocol skips its own TLS
		// setup once DialContext is set, so the handshake happens here.
		if tlsCfg != nil && protocol == clickhouse.Native {
			dial = tlsDialer(dial, tlsCfg)
			opts.TLS = nil
		}
		opts.DialContext = func(ctx context.Context, addr string) (net.Conn, error) {
			return dial(ctx, "tcp", addr)
		}
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return connectFailed([]string{addr}, err)
	}
	ctx, cancel := connectContext(ctx, t)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return connectFailed([]string{addr}, err)
	}
	d.conn = conn
	d.database = database
	d.resultCap = t.Config.EffectiveResultCap()
	return nil
}

func (d *clickhouseDriver) ListDatabases(ctx context.Context) ([]Database, error) {
	rows, err := d.conn.Query(ctx, "SELECT name, engine FROM system.databases ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var dbs []Database
	for rows.Next() {
		var name, engine string
		if err := rows.Scan(&name, &engine); err != nil {
			return nil, err
		}
		dbs = append(dbs, Database{Name: name, Detail: engine})
	}
	return dbs, rows.Err()
}

func (d *clickhouseDriver) ListTables(ctx context.Context) ([]Table, error) {
	const q = `SELECT name, engine, total_rows FROM system.tables WHERE database = ? ORDER BY name`
	rows, err := d.conn.Query(ctx, q, d.database)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tables []Table
	for rows.Next() {
		var name, engine string
		var totalRows *uint64
		if err := rows.Scan(&name, &engine, &totalRows); err != nil {
			return nil, err
		}
		t := Table{Name: name, Detail: engine}
		if totalRows != nil {
			t.Detail = fmt.Sprintf("%s, %d rows", engine, *totalRows)
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func (d *clickhouseDriver) ListColumns(ctx context.Context, table string) ([]Column, error) {
	database, name := splitQualified(table, d.database)
	const q = `SELECT name, type, default_expression FROM system.columns
WHERE database = ? AND table = ? ORDER BY position`
	rows, err := d.conn.Query(ctx, q, database, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []Column
	for rows.Next() {
		var c Column
		var def string
		if err := rows.Scan(&c.Name, &c.Type, &def); err != nil {
			return nil, err
		}
		// ClickHouse encodes optionality in the type itself.
		c.Nullable = strings.HasPrefix(c.Type, "Nullable(")
		if def != "" {
			c.Detail = "default " + def
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (d *clickhouseDriver) Execute(ctx context.Context, statement string) Result {
	return executeViaCursor(ctx, d, statement)
}

// ExecuteStream runs a statement and returns a cursor over its rows, leaving
// the ClickHouse result open until the cursor is closed or ctx is cancelled.
func (d *clickhouseDriver) ExecuteStream(ctx context.Context, statement string) (Cursor, error) {
	if !clickhouseReturnsRows(statement) {
		if err := d.conn.Exec(ctx, statement); err != nil {
			return nil, err
		}
		// No result set to page over.
		return newRowCursor(nil, nil, -1), nil
	}
	rows, err := d.conn.Query(ctx, statement)
	if err != nil {
		return nil, err
	}
	src := &chSource{rows: rows, types: rows.ColumnTypes()}
	return newCursor(ctx, rows.Columns(), d.resultCap, src), nil
}

// chSource pages a ClickHouse result. Each row is scanned into fresh values of
// the column types, because the driver scans into typed destinations rather
// than handing back an untyped row.
type chSource struct {
	rows  chdriver.Rows
	types []chdriver.ColumnType
}

func (s *chSource) fetch(n int) ([][]string, bool, error) {
	out := make([][]string, 0, min(n, 1024))
	for len(out) < n {
		if !s.rows.Next() {
			return out, true, s.rows.Err()
		}
		dest := make([]any, len(s.types))
		for i, t := range s.types {
			dest[i] = reflect.New(t.ScanType()).Interface()
		}
		if err := s.rows.Scan(dest...); err != nil {
			return out, true, err
		}
		row := make([]string, len(dest))
		for i, v := range dest {
			row[i] = stringify(reflect.ValueOf(v).Elem().Interface())
		}
		out = append(out, row)
	}
	return out, false, nil
}

// columnTypes reports the engine's own type names, so a paged result labels
// its headers the way ClickHouse spells them.
func (s *chSource) columnTypes() []string {
	out := make([]string, len(s.types))
	for i, t := range s.types {
		out[i] = t.DatabaseTypeName()
	}
	return out
}

func (s *chSource) release() error { return s.rows.Close() }

// affected: ClickHouse reports no row count for the statements that go through
// a result set.
func (s *chSource) affected() int64 { return -1 }

// clickhouseReturnsRows classifies a statement by its first keyword: queries
// go through Query, everything else through Exec.
func clickhouseReturnsRows(statement string) bool {
	words := topLevelSQLWords(statement, true)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXISTS", "WITH":
		return true
	}
	return false
}

func (d *clickhouseDriver) Close() error {
	if d.conn == nil {
		return nil
	}
	return d.conn.Close()
}
