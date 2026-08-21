package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/secrets"
	"github.com/lergor11/d9s/internal/session"
)

// statementWidth is how much of a statement a label or a diagnostic shows
// before it is elided.
const statementWidth = 100

// errReported marks a failure whose detail is already on stderr, so run turns
// it into an exit code without printing it a second time.
var errReported = errors.New("reported")

func runConnections(e env, o opts, format Format, pos []string) error {
	if err := checkArgs("connections", pos, 0, 0); err != nil {
		return err
	}
	cfg, err := loadConfig(e, o)
	if err != nil {
		return err
	}
	if len(cfg.Connections) == 0 {
		e.warn("no connections configured in %s", o.config)
	}
	res := db.Result{
		Columns:  []string{"name", "type", "host", "port", "database", "ssh", "tls"},
		Affected: -1,
	}
	for _, conn := range cfg.Connections {
		bastion := ""
		if conn.SSH != nil {
			bastion = conn.SSH.Bastion
		}
		res.Rows = append(res.Rows, []string{
			conn.Name,
			string(conn.Type),
			conn.Host,
			strconv.Itoa(conn.Port),
			conn.Database,
			bastion,
			string(conn.EffectiveTLSMode()),
		})
	}
	return e.write(format, res, "")
}

func runDatabases(ctx context.Context, e env, o opts, format Format, pos []string) error {
	if err := checkArgs("databases", pos, 1, 1); err != nil {
		return err
	}
	s, err := open(ctx, e, o, pos[0], "")
	if err != nil {
		return err
	}
	defer closeSession(e, s)

	dbs, err := s.Driver.ListDatabases(ctx)
	if err != nil {
		return fail(ExitQuery, "listing the databases of %q: %w", pos[0], err)
	}
	res := db.Result{Columns: []string{"name", "detail"}, Affected: -1}
	for _, d := range dbs {
		res.Rows = append(res.Rows, []string{d.Name, d.Detail})
	}
	return e.write(format, res, "")
}

func runTables(ctx context.Context, e env, o opts, format Format, pos []string) error {
	if err := checkArgs("tables", pos, 1, 2); err != nil {
		return err
	}
	database := ""
	if len(pos) == 2 {
		database = pos[1]
	}
	s, err := open(ctx, e, o, pos[0], database)
	if err != nil {
		return err
	}
	defer closeSession(e, s)

	tables, err := s.Driver.ListTables(ctx)
	if err != nil {
		return fail(ExitQuery, "listing the tables of %q: %w", pos[0], err)
	}
	res := db.Result{Columns: []string{"name", "detail"}, Affected: -1}
	for _, t := range tables {
		res.Rows = append(res.Rows, []string{t.Name, t.Detail})
	}
	return e.write(format, res, "")
}

func runDescribe(ctx context.Context, e env, o opts, format Format, pos []string) error {
	if err := checkArgs("describe", pos, 2, 2); err != nil {
		return err
	}
	s, err := open(ctx, e, o, pos[0], o.database)
	if err != nil {
		return err
	}
	defer closeSession(e, s)

	cols, err := s.Driver.ListColumns(ctx, pos[1])
	if err != nil {
		return fail(ExitQuery, "describing %q on %q: %w", pos[1], pos[0], err)
	}
	res := db.Result{Columns: []string{"name", "type", "nullable", "detail"}, Affected: -1}
	for _, c := range cols {
		res.Rows = append(res.Rows, []string{c.Name, c.Type, strconv.FormatBool(c.Nullable), c.Detail})
	}
	return e.write(format, res, "")
}

func runQuery(ctx context.Context, e env, o opts, format Format, pos []string) error {
	if err := checkArgs("query", pos, 1, 2); err != nil {
		return err
	}
	script, err := queryScript(e, o, pos)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(e, o)
	if err != nil {
		return err
	}
	conn, err := session.Find(cfg, pos[0])
	if err != nil {
		return &cmdError{code: ExitUsage, err: err}
	}

	stmts := db.Split(conn.Type, script)
	if len(stmts) == 0 {
		return fail(ExitUsage, "no statements to run")
	}
	// Checked before connecting, so a refused run touches nothing at all.
	if !o.write {
		if flagged := db.Destructive(conn.Type, stmts); len(flagged) > 0 {
			return fail(ExitRefused, "refusing to run %d destructive statement(s) without --write:\n  %s",
				len(flagged), strings.Join(labels(flagged), "\n  "))
		}
	}

	s, err := connect(ctx, conn, o.database)
	if err != nil {
		return err
	}
	defer closeSession(e, s)

	failures := 0
	for i, stmt := range stmts {
		if failures > 0 {
			e.warn("not run: %s", clip(stmt, statementWidth))
			continue
		}
		res := s.Driver.Execute(ctx, stmt)
		if res.Err != nil {
			e.warn("%s: %s", clip(stmt, statementWidth), res.Err)
			failures++
			continue
		}
		label := ""
		if len(stmts) > 1 {
			label = fmt.Sprintf("[%d] %s", i+1, clip(stmt, statementWidth))
		}
		if i > 0 {
			// Reaching statement i means every earlier one was written.
			e.separate(format)
		}
		if err := e.write(format, res, label); err != nil {
			return err
		}
		e.summarize(res)
	}
	if failures > 0 {
		return &cmdError{code: ExitQuery, err: fmt.Errorf("%w: a statement failed", errReported)}
	}
	return nil
}

// queryScript returns the SQL to run: the positional argument, the -f file, or
// stdin, in that order. Naming more than one source is an error rather than a
// silent preference, because the caller then does not know which one ran.
func queryScript(e env, o opts, pos []string) (string, error) {
	inline := ""
	if len(pos) == 2 {
		inline = pos[1]
	}
	switch {
	case inline != "" && o.file != "":
		return "", fail(ExitUsage, "the SQL came both as an argument and from -f %s; give one of them", o.file)
	case inline != "":
		return inline, nil
	case o.file != "":
		data, err := os.ReadFile(o.file) //nolint:gosec // the path comes from the caller's own command line
		if err != nil {
			return "", fail(ExitUsage, "reading the SQL: %w", err)
		}
		return string(data), nil
	case e.stdinTerminal:
		return "", fail(ExitUsage, "no SQL given: pass it as an argument, with -f file, or on stdin")
	default:
		data, err := io.ReadAll(e.stdin)
		if err != nil {
			return "", fail(ExitUsage, "reading the SQL from stdin: %w", err)
		}
		return string(data), nil
	}
}

// labels renders statements for an error message, one elided line each.
func labels(stmts []string) []string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = clip(s, statementWidth)
	}
	return out
}

// open looks the connection up and connects to it, which is what every command
// but `connections` starts with.
func open(ctx context.Context, e env, o opts, name, database string) (*session.Session, error) {
	cfg, err := loadConfig(e, o)
	if err != nil {
		return nil, err
	}
	conn, err := session.Find(cfg, name)
	if err != nil {
		return nil, &cmdError{code: ExitUsage, err: err}
	}
	return connect(ctx, conn, database)
}

// connect opens the session, turning a failure into the connection exit code.
// An empty database leaves the driver on the connection's configured one.
func connect(ctx context.Context, conn config.Connection, database string) (*session.Session, error) {
	s, err := session.Open(ctx, secrets.NewResolver(), conn, database)
	if err != nil {
		return nil, &cmdError{code: ExitConnect, err: err}
	}
	return s, nil
}

// closeSession releases the session, reporting a failure on stderr: the data
// is already written by then, so a close error must not change the exit code.
func closeSession(e env, s *session.Session) {
	if err := s.Close(); err != nil {
		e.warn("closing the session: %s", err)
	}
}
