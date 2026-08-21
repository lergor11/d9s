// Package cli implements the non-interactive subcommands of d9s, so the
// connections configured for the interface — 1Password secrets, SSH bastions,
// TLS and all — can also be used from a script, a pipeline, or an agent.
//
// Every command writes its data to stdout and everything else to stderr, and
// returns one of the Exit codes below rather than calling os.Exit, so the
// commands stay testable.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/lergor11/d9s/internal/config"
)

// Exit codes returned by the subcommands. Scripts branch on these, so their
// meanings are part of the command-line contract and must not be reshuffled.
const (
	// ExitOK reports that the command completed.
	ExitOK = 0
	// ExitError reports a failure that is none of the categories below, such
	// as stdout going away mid-write.
	ExitError = 1
	// ExitUsage reports a bad command line or an unusable configuration.
	ExitUsage = 2
	// ExitConnect reports that the database could not be reached.
	ExitConnect = 3
	// ExitQuery reports that a statement or a catalog lookup failed while the
	// connection itself was healthy.
	ExitQuery = 4
	// ExitRefused reports a destructive statement that was not run because
	// --write was absent. Nothing executed.
	ExitRefused = 5
)

// Connections lists the configured connections.
func Connections(args []string) int { return run("connections", args, osEnv()) }

// Databases lists the databases of one connection.
func Databases(args []string) int { return run("databases", args, osEnv()) }

// Tables lists the tables of one database, or the key prefixes of a Redis
// connection.
func Tables(args []string) int { return run("tables", args, osEnv()) }

// Describe lists the columns of one table, or the keys under one Redis prefix.
func Describe(args []string) int { return run("describe", args, osEnv()) }

// Query runs SQL taken from an argument, a file, or stdin.
func Query(args []string) int { return run("query", args, osEnv()) }

// Names returns the subcommands this package implements, in the order they
// should be listed. Commands served from elsewhere, such as the MCP server,
// are absent from it but still carry a Synopsis and a Summary.
func Names() []string { return []string{"connections", "databases", "tables", "describe", "query"} }

// env is the process environment a command reads and writes, injected so the
// tests can drive a command without a terminal or a real stdout.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// terminal reports whether stdout is attached to a terminal, which picks
	// the default output format.
	terminal bool
	// stdinTerminal reports whether stdin is a terminal, which is how `query`
	// tells "no SQL was given" from "SQL is being piped in".
	stdinTerminal bool
	// width is the terminal width in columns; 0 means unknown, and leaves the
	// table renderer to use its own column cap.
	width int
}

func osEnv() env {
	out := int(os.Stdout.Fd())
	e := env{
		stdin:         os.Stdin,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
		terminal:      term.IsTerminal(out),
		stdinTerminal: term.IsTerminal(int(os.Stdin.Fd())),
	}
	if e.terminal {
		if w, _, err := term.GetSize(out); err == nil {
			e.width = w
		}
	}
	return e
}

// warn writes one diagnostic line to stderr, prefixed like every other d9s
// diagnostic. Stderr is best-effort: a command's exit code must not depend on
// whether its notices could be written.
func (e env) warn(format string, a ...any) {
	_, _ = fmt.Fprintf(e.stderr, "d9s: "+format+"\n", a...)
}

// cmdError couples a failure with the exit code it produces.
type cmdError struct {
	code int
	err  error
}

func (e *cmdError) Error() string { return e.err.Error() }
func (e *cmdError) Unwrap() error { return e.err }

// fail builds an error carrying the exit code the command should return.
func fail(code int, format string, a ...any) error {
	return &cmdError{code: code, err: fmt.Errorf(format, a...)}
}

// exitCode reports the code an error should end the process with.
func exitCode(err error) int {
	var ce *cmdError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ExitError
}

// opts holds every flag the subcommands accept. Each command registers only
// the ones it understands, so `d9s tables -f x` is a usage error rather than a
// silently ignored flag.
type opts struct {
	config   string
	format   string
	file     string
	database string
	write    bool
	timeout  time.Duration
}

// run parses the command line of one subcommand, executes it, and returns the
// process exit code. Failures are reported on stderr, prefixed like every
// other d9s diagnostic.
func run(name string, args []string, e env) int {
	err := execute(name, args, e)
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, flag.ErrHelp) {
		// The flag set already printed the usage text.
		return ExitUsage
	}
	if !errors.Is(err, errReported) {
		e.warn("%s", err)
	}
	return exitCode(err)
}

func execute(name string, args []string, e env) error {
	var o opts
	fs := flag.NewFlagSet("d9s "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.StringVar(&o.config, "config", config.DefaultPath(), "configuration file")
	fs.StringVar(&o.format, "o", "", "output format: table, csv, json, or jsonl")
	fs.DurationVar(&o.timeout, "timeout", 0, "give up after this long (0 = no limit)")
	if name == "describe" || name == "query" {
		fs.StringVar(&o.database, "database", "", "database to use (default: the connection's)")
	}
	if name == "query" {
		fs.StringVar(&o.file, "f", "", "read the SQL from this file")
		fs.BoolVar(&o.write, "write", false, "allow destructive statements to run")
	}
	fs.Usage = func() { _, _ = fmt.Fprint(e.stderr, commandUsage(name)) }

	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	format, err := resolveFormat(o.format, e.terminal)
	if err != nil {
		return err
	}

	ctx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	switch name {
	case "connections":
		return runConnections(e, o, format, pos)
	case "databases":
		return runDatabases(ctx, e, o, format, pos)
	case "tables":
		return runTables(ctx, e, o, format, pos)
	case "describe":
		return runDescribe(ctx, e, o, format, pos)
	case "query":
		return runQuery(ctx, e, o, format, pos)
	default:
		return fail(ExitUsage, "unknown command %q (want %s)", name, strings.Join(Names(), ", "))
	}
}

// parseArgs parses flags that appear before, between, and after the positional
// arguments, so `d9s query prod-pg 'SELECT 1' -o csv` behaves like
// `d9s query -o csv prod-pg 'SELECT 1'`. Everything after a bare `--` is
// positional, which is how SQL that starts with a dash gets through.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			// The flag set already described the offending flag.
			return nil, &cmdError{code: ExitUsage, err: err}
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	return append(positional, tail...), nil
}

// checkArgs rejects a wrong number of positional arguments, pointing at the
// command's own usage line rather than at a generic one.
func checkArgs(name string, pos []string, minArgs, maxArgs int) error {
	if len(pos) < minArgs {
		return fail(ExitUsage, "%s needs more arguments\n\n%s", name, commandUsage(name))
	}
	if len(pos) > maxArgs {
		return fail(ExitUsage, "%s takes at most %d argument(s), got %d\n\n%s",
			name, maxArgs, len(pos), commandUsage(name))
	}
	return nil
}

// loadConfig reads the configuration and reports its warnings on stderr, where
// they cannot corrupt the data on stdout.
func loadConfig(e env, o opts) (*config.Config, error) {
	cfg, warns, err := config.Load(o.config)
	if err != nil {
		return nil, &cmdError{code: ExitUsage, err: err}
	}
	for _, w := range warns {
		e.warn("warning: %s: %s", w.Connection, w.Message)
	}
	return cfg, nil
}
