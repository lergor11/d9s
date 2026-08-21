package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
)

// noArgs is the argument type of a tool that takes none. The SDK infers the
// empty object schema the protocol requires from it.
type noArgs struct{}

// connectionArgs names a connection.
type connectionArgs struct {
	Connection string `json:"connection" jsonschema:"name of a configured connection, as returned by list_connections"`
}

// databaseArgs names a connection and, optionally, a database inside it.
type databaseArgs struct {
	Connection string `json:"connection" jsonschema:"name of a configured connection, as returned by list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database to use; omit to use the connection's configured default"`
}

// describeArgs names one table to describe.
type describeArgs struct {
	Connection string `json:"connection" jsonschema:"name of a configured connection, as returned by list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database holding the table; omit to use the connection's configured default"`
	Table      string `json:"table" jsonschema:"table to describe, spelled exactly as list_tables returned it, including any schema prefix"`
}

// queryArgs carries the statement to run.
type queryArgs struct {
	Connection string `json:"connection" jsonschema:"name of a configured connection, as returned by list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database to run against; omit to use the connection's configured default"`
	SQL        string `json:"sql" jsonschema:"the statement to run: SQL for postgres and clickhouse, one command per line for redis. Read-only unless the connection and the server both permit writes. Add an explicit LIMIT: results are capped at 200 rows and 100 KB"`
}

// readOnly marks a tool that cannot change the database, which lets a client
// run it without prompting.
var readOnly = &mcpsdk.ToolAnnotations{ReadOnlyHint: true}

// register adds the five tools to the SDK server. The descriptions are written
// for an agent choosing between them, not for a changelog.
func (s *Server) register(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_connections",
		Title:       "List database connections",
		Annotations: readOnly,
		Description: "List the databases this server can reach, with their engine, address and whether writes are permitted. " +
			"Call this first: every other tool takes one of these names. Passwords are shown as the op:// or ${ENV} reference they come from, never as values.",
	}, guard(s, "list_connections", s.listConnections))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_databases",
		Title:       "List databases on a connection",
		Annotations: readOnly,
		Description: "List the databases available on one connection. Use it when you need a database name for the other tools; " +
			"for redis it lists the numbered logical databases.",
	}, guard(s, "list_databases", s.listDatabases))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_tables",
		Title:       "List tables in a database",
		Annotations: readOnly,
		Description: "List the tables of a database, with a row estimate where the engine offers one. " +
			"For redis it lists key prefixes instead. Do this before writing a query, so you use names that exist.",
	}, guard(s, "list_tables", s.listTables))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "describe_table",
		Title:       "Describe a table",
		Annotations: readOnly,
		Description: "Show the columns of one table: name, type, nullability and default. " +
			"For redis it lists the keys under a prefix. Describe a table before querying it rather than guessing column names.",
	}, guard(s, "describe_table", s.describeTable))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "query",
		Title:       "Run a query",
		Annotations: readOnly,
		Description: "Run a read-only statement and return its rows. Destructive statements (DROP, TRUNCATE, ALTER, " +
			"unqualified DELETE or UPDATE, redis FLUSHALL/DEL and friends) are refused server-side unless the operator " +
			"has explicitly opened both halves of the write gate. Results are capped at 200 rows and 100 KB and say so " +
			"when they are cut, so add a LIMIT and select the columns you need.",
	}, guard(s, "query", s.query))
}

// lookup finds a connection the server is allowed to use. A connection held
// back by the allowlist is reported exactly like one that was never
// configured, which is what makes the allowlist a boundary and not a hint.
func (s *Server) lookup(name string) (config.Connection, error) {
	conn, ok := s.byName[name]
	if !ok {
		return config.Connection{}, fmt.Errorf("unknown connection %q; call list_connections for the names this server exposes", name)
	}
	return conn, nil
}

// listConnections reports what the server exposes, in configuration order.
func (s *Server) listConnections(_ context.Context, _ noArgs) (string, error) {
	if len(s.conns) == 0 {
		return "No connections are configured. Add one to the d9s config file, or check the --connections allowlist this server was started with.", nil
	}
	columns := []string{"NAME", "ENGINE", "ADDRESS", "DATABASE", "SSH", "TLS", "PASSWORD", "WRITES"}
	rows := make([][]string, 0, len(s.conns))
	for _, conn := range s.conns {
		writes := "read-only"
		if s.allowWrite && conn.AllowWrite {
			writes = "allowed"
		}
		rows = append(rows, []string{
			conn.Name,
			string(conn.Type),
			endpoint(conn),
			orDash(conn.Database),
			sshLabel(conn),
			string(conn.EffectiveTLSMode()),
			passwordRef(conn),
			writes,
		})
	}
	return grid(columns, rows) + "\n" + s.writeGateNote(), nil
}

// writeGateNote states the write contract as it currently stands, so an agent
// reading list_connections learns it without loading the skill.
func (s *Server) writeGateNote() string {
	if s.allowWrite {
		return "Writes need --allow-write (given) and allow_write: true on the connection; connections marked read-only above lack the second.\n"
	}
	return "All connections are read-only: this server was started without --allow-write, so destructive statements are refused whatever a connection configures.\n"
}

// listDatabases enumerates the databases of a connection.
func (s *Server) listDatabases(ctx context.Context, in connectionArgs) (string, error) {
	sess, err := s.session(ctx, in.Connection, "")
	if err != nil {
		return "", err
	}
	dbs, err := sess.driver.ListDatabases(ctx)
	if err != nil {
		return "", fmt.Errorf("listing the databases of %q: %w", in.Connection, err)
	}
	rows := make([][]string, len(dbs))
	for i, d := range dbs {
		rows[i] = []string{d.Name, d.Detail}
	}
	return grid([]string{"NAME", "DETAIL"}, rows), nil
}

// listTables enumerates the tables of one database.
func (s *Server) listTables(ctx context.Context, in databaseArgs) (string, error) {
	sess, err := s.session(ctx, in.Connection, in.Database)
	if err != nil {
		return "", err
	}
	tables, err := sess.driver.ListTables(ctx)
	if err != nil {
		return "", fmt.Errorf("listing the tables of %q: %w", in.Connection, err)
	}
	rows := make([][]string, len(tables))
	for i, t := range tables {
		rows[i] = []string{t.Name, t.Detail}
	}
	return grid([]string{"NAME", "DETAIL"}, rows), nil
}

// describeTable reports the columns of one table.
func (s *Server) describeTable(ctx context.Context, in describeArgs) (string, error) {
	if strings.TrimSpace(in.Table) == "" {
		return "", errors.New("table is empty; pass a name from list_tables")
	}
	sess, err := s.session(ctx, in.Connection, in.Database)
	if err != nil {
		return "", err
	}
	cols, err := sess.driver.ListColumns(ctx, in.Table)
	if err != nil {
		return "", fmt.Errorf("describing %q on %q: %w", in.Table, in.Connection, err)
	}
	rows := make([][]string, len(cols))
	for i, c := range cols {
		nullable := "not null"
		if c.Nullable {
			nullable = "null"
		}
		rows[i] = []string{c.Name, c.Type, nullable, c.Detail}
	}
	return grid([]string{"NAME", "TYPE", "NULLABLE", "DETAIL"}, rows), nil
}

// query runs the statements in the request, refusing the destructive ones
// unless both halves of the write gate are open. The gate is checked over the
// whole script before anything executes, so a destructive statement cannot
// slip through behind a harmless one.
func (s *Server) query(ctx context.Context, in queryArgs) (string, error) {
	conn, err := s.lookup(in.Connection)
	if err != nil {
		return "", err
	}
	stmts := db.Split(conn.Type, in.SQL)
	if len(stmts) == 0 {
		return "", errors.New("sql holds no executable statement")
	}
	if destructive := db.Destructive(conn.Type, stmts); len(destructive) > 0 {
		if err := s.checkWrite(conn, destructive[0]); err != nil {
			return "", err
		}
	}
	sess, err := s.pool.get(ctx, conn, in.Database)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for i, stmt := range stmts {
		if i > 0 {
			b.WriteString("\n")
		}
		text, err := runStatement(ctx, sess.driver, stmt)
		b.WriteString(text)
		if err != nil {
			b.WriteString(formatFailure(stmt, err))
			for _, rest := range stmts[i+1:] {
				b.WriteString("\n" + formatSkipped(rest))
			}
			break
		}
	}
	return b.String(), nil
}

// runStatement executes one statement and renders at most a capped page of its
// result. It streams: the cursor stops reading once the row cap is met, so a
// `SELECT *` against a large table costs one page rather than the whole result
// buffered in memory and then thrown away. A driver with no streaming of its
// own still works — db.Stream buffers for it — so the caller has one path.
//
// A returned error means the statement failed; the caller renders it, having
// the statement text to hand.
func runStatement(ctx context.Context, driver db.Driver, statement string) (string, error) {
	started := time.Now()
	cursor, err := db.Stream(ctx, driver, statement)
	if err != nil {
		return "", err
	}
	defer func() { _ = cursor.Close() }()

	cursor.SetCap(MaxRows)
	rows, err := cursor.NextPage(MaxRows)
	if err != nil {
		return "", err
	}
	columns := cursor.Columns()
	if len(columns) > 0 {
		return formatPage(statement, columns, rows, cursor.Truncated(), -1, time.Since(started)), nil
	}
	// A statement that returned no columns changed rows instead, and the count
	// is only meaningful once the engine's result is drained.
	if err := cursor.Close(); err != nil {
		return "", err
	}
	return formatPage(statement, nil, nil, false, cursor.Affected(), time.Since(started)), nil
}

// checkWrite applies the write gate. Both switches are required, and the
// refusal names the ones that are missing, because an operator who opened only
// one has to know which.
func (s *Server) checkWrite(conn config.Connection, stmt string) error {
	if s.allowWrite && conn.AllowWrite {
		return nil
	}
	var missing []string
	if !s.allowWrite {
		missing = append(missing, "this server was started without --allow-write")
	}
	if !conn.AllowWrite {
		missing = append(missing, fmt.Sprintf("connection %q does not set allow_write: true in the d9s config", conn.Name))
	}
	return fmt.Errorf("refused, nothing was executed: %q is destructive, and a write needs both --allow-write at launch and allow_write: true on the connection, but %s",
		oneLine(stmt), strings.Join(missing, ", and "))
}

// session resolves a connection name and returns its pooled session.
func (s *Server) session(ctx context.Context, name, database string) (*session, error) {
	conn, err := s.lookup(name)
	if err != nil {
		return nil, err
	}
	return s.pool.get(ctx, conn, database)
}

// endpoint renders where a connection points, without reaching it.
func endpoint(conn config.Connection) string {
	if conn.IsUnixSocket() {
		return conn.Host
	}
	return net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
}

// sshLabel names the bastion a connection hops through, if any.
func sshLabel(conn config.Connection) string {
	if conn.SSH == nil {
		return "-"
	}
	return "via " + conn.SSH.Bastion
}

// passwordRef renders how a connection's password is obtained, never the value
// itself. A reference is safe to echo; a literal written into the config file
// is a secret, so it is named but withheld.
func passwordRef(conn config.Connection) string {
	switch {
	case conn.Password == "":
		return "none"
	case config.IsOpRef(conn.Password), config.IsEnvRef(conn.Password):
		return conn.Password
	default:
		return "(literal in config; withheld)"
	}
}

// orDash renders an unset optional field.
func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}
