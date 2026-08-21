package cli

import "strings"

// Summaries describes each subcommand in one line, for the `d9s --help`
// listing. The order matches Names.
func Summaries() map[string]string {
	return map[string]string{
		"connections": "list the configured connections",
		"databases":   "list the databases of a connection",
		"tables":      "list the tables of a database",
		"describe":    "list the columns of a table",
		"query":       "run SQL from an argument, a file, or stdin",
	}
}

// synopsis is the argument line of each subcommand, without the flags.
var synopsis = map[string]string{
	"connections": "d9s connections",
	"databases":   "d9s databases <connection>",
	"tables":      "d9s tables <connection> [database]",
	"describe":    "d9s describe <connection> <table>",
	"query":       "d9s query <connection> [sql]",
}

// Synopsis returns the argument line of one subcommand, for the top-level
// help. A name this package does not implement renders as a bare invocation.
func Synopsis(name string) string {
	if s, ok := synopsis[name]; ok {
		return s
	}
	return "d9s " + name
}

// ExitCodeHelp returns the exit-code table, so the top-level help documents
// the same codes the subcommands do.
func ExitCodeHelp() string { return exitCodeHelp }

// details is what each subcommand's own --help adds below its flag list.
var details = map[string]string{
	"connections": `Reads the configuration only; it never connects.
`,
	"databases": `Lists the logical databases the connection can see. Redis reports its
sixteen numbered databases, or only 0 in cluster mode.
`,
	"tables": `Without a database argument the connection's configured database is used.
For Redis the listing is of key prefixes rather than tables.
`,
	"describe": `Lists the columns of the table: name, type, nullability, and the default
or other engine detail. For Redis it lists the keys under the prefix.
`,
	"query": `The SQL comes from the positional argument, from -f, or from stdin, in
that order; giving more than one of them is an error. A script may hold
several ;-separated statements, which run in order and stop at the first
failure. SQL that begins with a dash needs a -- of its own first, so the
flag parser leaves it alone:

  d9s query prod-pg -- '-- a leading comment
  SELECT 1'

Destructive statements — DROP, TRUNCATE, ALTER, DELETE or UPDATE without
a WHERE, FLUSHALL and friends — are refused unless --write is given,
because there is no prompt to answer out here. Nothing runs when one is
refused, not even the harmless statements beside it.
`,
}

// commandUsage renders the help of one subcommand: what it takes, the flags it
// accepts, and the exit codes it can produce.
func commandUsage(name string) string {
	var b strings.Builder
	b.WriteString("Usage:\n  " + synopsis[name] + " [flags]\n\nFlags:\n")
	b.WriteString("  -config path   configuration file\n")
	b.WriteString("  -o format      table, csv, json, or jsonl\n")
	b.WriteString("                 (default: table on a terminal, jsonl when piped)\n")
	b.WriteString("  -timeout dur   give up after dur, e.g. 30s (default: no limit)\n")
	switch name {
	case "describe":
		b.WriteString("  -database name database holding the table\n")
	case "query":
		b.WriteString("  -database name database to run against\n")
		b.WriteString("  -f file        read the SQL from this file\n")
		b.WriteString("  --write        allow destructive statements to run\n")
	}
	if d := details[name]; d != "" {
		b.WriteString("\n" + d)
	}
	b.WriteString("\nExit codes:\n" + exitCodeHelp)
	return b.String()
}

// exitCodeHelp documents the exit codes. It is shown by every subcommand's
// help and by the top-level one, because branching on them is the reason the
// commands exist.
const exitCodeHelp = `  0  success
  2  usage error, including an unknown connection or a bad configuration
  3  the database could not be reached
  4  a statement or a catalog lookup failed
  5  a destructive statement was refused; pass --write to allow it
`
