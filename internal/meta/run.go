package meta

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// Run answers one parsed command from the driver's catalog and returns an
// ordinary result table, so export, copy, sorting and filtering apply to it
// unchanged. `\q` never reaches Run: quitting is the caller's decision.
func Run(ctx context.Context, driver db.Driver, engine config.EngineType, c Command, stmt string) db.Result {
	start := time.Now()
	res := answer(ctx, driver, engine, c)
	res.Statement = stmt
	if res.Affected == 0 {
		res.Affected = -1
	}
	res.Duration = time.Since(start)
	return res
}

func answer(ctx context.Context, driver db.Driver, engine config.EngineType, c Command) db.Result {
	switch c.Verb {
	case "?":
		return db.Result{Columns: []string{"command", "description"}, Rows: Help()}
	case "l":
		return listDatabases(ctx, driver)
	case "dt":
		return listTables(ctx, driver)
	case "d":
		if c.Arg == "" {
			return listTables(ctx, driver)
		}
		return describeTable(ctx, driver, c)
	case "dn", "du", "df":
		return catalog(ctx, driver, engine, c.Verb)
	default:
		return db.Result{Err: unknownVerb(c.Verb)}
	}
}

func listDatabases(ctx context.Context, driver db.Driver) db.Result {
	dbs, err := driver.ListDatabases(ctx)
	if err != nil {
		return db.Result{Err: err}
	}
	res := db.Result{Columns: []string{"name", "detail"}}
	for _, d := range dbs {
		res.Rows = append(res.Rows, []string{d.Name, d.Detail})
	}
	return res
}

func listTables(ctx context.Context, driver db.Driver) db.Result {
	tables, err := driver.ListTables(ctx)
	if err != nil {
		return db.Result{Err: err}
	}
	res := db.Result{Columns: []string{"name", "detail"}}
	for _, t := range tables {
		res.Rows = append(res.Rows, []string{t.Name, t.Detail})
	}
	return res
}

// describeTable lists a table's columns; with the + flag it appends the
// engine's extra detail — indexes, size, comment — as rows of the same table,
// so the answer stays one exportable result.
func describeTable(ctx context.Context, driver db.Driver, c Command) db.Result {
	cols, err := driver.ListColumns(ctx, c.Arg)
	if err != nil {
		return db.Result{Err: err}
	}
	res := db.Result{Columns: []string{"column", "type", "nullable", "detail"}}
	for _, col := range cols {
		res.Rows = append(res.Rows, []string{col.Name, col.Type, strconv.FormatBool(col.Nullable), col.Detail})
	}
	if !c.Plus {
		return res
	}
	desc, ok := driver.(db.Describer)
	if !ok {
		// The engine has no extra detail to add; the plain description is the
		// whole answer rather than an error.
		return res
	}
	det, err := desc.DescribeTable(ctx, c.Arg)
	if err != nil {
		return db.Result{Err: err}
	}
	for _, ix := range det.Indexes {
		res.Rows = append(res.Rows, []string{ix.Name, "index", "", ix.Definition})
	}
	if det.Size != "" {
		res.Rows = append(res.Rows, []string{"(size)", "", "", det.Size})
	}
	if det.Comment != "" {
		res.Rows = append(res.Rows, []string{"(comment)", "", "", det.Comment})
	}
	return res
}

// catalogQueries maps a verb onto the engine's own catalog, for the verbs the
// generic driver interface does not cover.
var catalogQueries = map[config.EngineType]map[string]string{
	config.Postgres: {
		"dn": `SELECT nspname AS name, pg_get_userbyid(nspowner) AS owner
		       FROM pg_namespace
		       WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
		       ORDER BY 1`,
		"du": `SELECT rolname AS name, rolsuper AS superuser, rolcreatedb AS createdb, rolcanlogin AS login
		       FROM pg_roles ORDER BY 1`,
		"df": `SELECT n.nspname AS schema, p.proname AS name,
		              pg_get_function_result(p.oid) AS result,
		              pg_get_function_arguments(p.oid) AS arguments
		       FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		       WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		       ORDER BY 1, 2`,
	},
	config.ClickHouse: {
		"du": `SELECT name, storage FROM system.users ORDER BY name`,
		"df": `SELECT name, is_aggregate FROM system.functions ORDER BY name`,
	},
}

// unsupported says plainly that an engine cannot answer a verb, naming the
// engine, so an empty result is never mistaken for an answer.
var unsupported = map[config.EngineType]map[string]string{
	config.ClickHouse: {
		"dn": `clickhouse has no schemas; its databases play that role (try \l)`,
	},
	config.Redis: {
		"dn": "redis has no schemas to list",
		"du": "redis has no roles to list",
		"df": "redis has no functions to list",
	},
}

func catalog(ctx context.Context, driver db.Driver, engine config.EngineType, verb string) db.Result {
	if msg, ok := unsupported[engine][verb]; ok {
		return db.Result{Err: fmt.Errorf("%s", msg)}
	}
	query, ok := catalogQueries[engine][verb]
	if !ok {
		return db.Result{Err: fmt.Errorf(`%s cannot answer \%s`, engine, verb)}
	}
	res := driver.Execute(ctx, query)
	// The catalog query is an implementation detail; the command is what ran.
	res.Statement = ""
	return res
}
