// Command d9s is a terminal UI for PostgreSQL, ClickHouse, and Redis, with
// SSH bastion support and 1Password-backed secrets. It also runs each of its
// views as a subcommand, so the same connections work from a script.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/template"

	"github.com/lergor11/d9s/internal/cli"
	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/mcp"
	"github.com/lergor11/d9s/internal/ui"
)

// Build metadata, stamped at link time with -ldflags -X. The defaults are what
// a bare `go build` produces; `make build` substitutes a development string
// carrying the short commit, and GoReleaser substitutes the released tag.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// subcommands are the non-interactive commands, each returning the exit code
// of the process. A bare `d9s`, or one whose first argument is a flag, opens
// the interactive interface instead.
//
// Add a subcommand by adding one line here; the help text and the
// unknown-command error pick it up from the listing below.
var subcommands = map[string]func(args []string) int{
	"connections": cli.Connections,
	"databases":   cli.Databases,
	"tables":      cli.Tables,
	"describe":    cli.Describe,
	"query":       cli.Query,
	"mcp":         func(args []string) int { return mcp.Main(args, version) },
}

// commandOrder lists the subcommands in the order the help shows them; any
// name missing from it is appended alphabetically, so a newly registered
// command is still documented.
var commandOrder = cli.Names()

// usageText is printed by --help. The config path is filled in so the user
// sees where their own file belongs, not a generic placeholder.
const usageText = `d9s {{.Version}} — a k9s-style terminal UI for databases.

Usage:
  d9s [flags]                    open the interactive interface
  d9s <command> [flags] [args]   run one command and exit

Flags:
  -config path   configuration file (default {{.ConfigPath}})
  -version       print the version and exit
  -help          print this help and exit

Commands (each takes -help of its own):
{{range .Commands}}  {{printf "%-34s" .Synopsis}} {{.Summary}}
{{end}}
Flags of the listing and query commands:
  -config path   configuration file
  -o format      table, csv, json, or jsonl
                 (default: table on a terminal, jsonl when piped)
  -timeout dur   give up after dur, e.g. 30s (default: no limit)
  -database name database to use (describe, query)
  -f file        read the SQL from this file (query)
  --write        allow destructive statements to run (query)

  Data goes to stdout and everything else to stderr, so a pipeline reads
  clean rows:
    d9s query prod-pg 'SELECT id FROM users LIMIT 5' | jq -r .id

  Statements may be psql-style meta-commands, answered from the catalog:
    d9s query prod-pg '\dt'       list tables (also \l, \d+ <table>, \? ...)

Exit codes:
{{.ExitCodes}}
Configuration:
  Connections live in a YAML file, by default {{.ConfigPath}}
  (override with -config or the D9S_CONFIG environment variable).
  Starting d9s without one is safe: it shows where the file goes and what
  it should contain.

  Passwords must be references, never literals:
    op://Vault/item/password   read through the 1Password CLI at connect time
    ${ENV_VAR}                 read from the environment

  Add an ssh block to a connection to reach it through a bastion; the key
  comes from the 1Password SSH agent, so no private key is ever read by d9s.

Keys (press ? inside d9s for the bindings of the current view):
  j/k, arrows    move            enter   connect / open
  ctrl+r, F5     run buffer      esc     back one level
  alt+enter      run buffer (see the README for shift+enter)
  ctrl+x         cancel query    ctrl+j  toggle editor/results focus
  tab            complete the name at the cursor (ctrl+g reloads names)
  ctrl+h         query history   s       schema panel
  e / y          export to file / copy to clipboard
  ? / q          help / quit
  In the editor, \dt, \d+ <table>, \l and friends answer from the catalog
  (\? lists them, \q quits).

Try it against throwaway containers:
  docker run -d --rm --name d9s-pg -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
  DEMO_PG_PASSWORD=secret d9s -config examples/demo-docker.yaml
`

// commandHelp is one line of the Commands section of the help.
type commandHelp struct{ Synopsis, Summary string }

// helpCommands renders the registered subcommands in listing order.
func helpCommands() []commandHelp {
	names := make([]string, 0, len(subcommands))
	for _, n := range commandOrder {
		if _, ok := subcommands[n]; ok {
			names = append(names, n)
		}
	}
	rest := make([]string, 0, len(subcommands))
	for n := range subcommands {
		if !slices.Contains(names, n) {
			rest = append(rest, n)
		}
	}
	slices.Sort(rest)
	summaries := cli.Summaries()
	out := make([]commandHelp, 0, len(subcommands))
	for _, n := range append(names, rest...) {
		out = append(out, commandHelp{Synopsis: cli.Synopsis(n), Summary: summaries[n]})
	}
	return out
}

func usage(out io.Writer, configPath string) {
	t := template.Must(template.New("usage").Parse(usageText))
	data := struct {
		Version, ConfigPath, ExitCodes string
		Commands                       []commandHelp
	}{version, configPath, cli.ExitCodeHelp(), helpCommands()}
	if err := t.Execute(out, data); err != nil {
		fmt.Fprintln(os.Stderr, "d9s: rendering help:", err)
	}
}

func main() {
	defaultConfig := config.DefaultPath()
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(dispatch(os.Args[1], os.Args[2:], defaultConfig, os.Stdout, os.Stderr))
	}

	cfgPath := flag.String("config", defaultConfig, "path to config.yaml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() { usage(os.Stderr, defaultConfig) }
	flag.Parse()

	if *showVersion {
		fmt.Printf("d9s %s\ncommit: %s\nbuilt:  %s\n", version, commit, date)
		return
	}

	cfg, warns, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "d9s:", err)
		os.Exit(1)
	}

	if err := ui.Run(cfg, warns, *cfgPath, version); err != nil {
		fmt.Fprintln(os.Stderr, "d9s:", err)
		os.Exit(1)
	}
}

// dispatch runs one subcommand and returns the process exit code. The writers
// carry the help and the unknown-command error; a subcommand writes its own
// output to the process streams.
func dispatch(name string, args []string, configPath string, stdout, stderr io.Writer) int {
	if name == "help" {
		usage(stdout, configPath)
		return cli.ExitOK
	}
	if run, ok := subcommands[name]; ok {
		return run(args)
	}
	_, _ = fmt.Fprintf(stderr, "d9s: unknown command %q (want %s)\n", name, strings.Join(commandNames(), ", "))
	return cli.ExitUsage
}

// commandNames lists the registered subcommands for an error message.
func commandNames() []string {
	names := make([]string, 0, len(subcommands))
	for n := range subcommands {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
