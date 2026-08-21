// Package mcp serves d9s's configured connections to an MCP client, so an
// agent can explore a schema and run queries while the credentials stay in
// 1Password and never enter the agent's context.
//
// The server is read-only unless both halves of the write gate are open, every
// response is bounded, and no resolved secret reaches a response, an error or
// a log line. Diagnostics go to stderr, because stdout carries the protocol.
package mcp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/secrets"
)

// instructions are handed to the client at initialize, so an agent that never
// loads the bundled skill still learns the contract.
const instructions = `d9s exposes the databases configured in the user's d9s config. ` +
	`Credentials stay in 1Password: you never see or need them. ` +
	`Start with list_connections, then list_tables and describe_table, and only then query. ` +
	`query is read-only — destructive statements are refused server-side. ` +
	`Every response is capped at 200 rows and 100 KB, so prefer a WHERE clause and an explicit LIMIT.`

// Options configures a Server.
type Options struct {
	// Config holds the connections the server may expose, before Connections
	// narrows them.
	Config *config.Config
	// Connections is the allowlist: only these may be used, and the rest are
	// invisible. Empty means every configured connection.
	Connections []string
	// AllowWrite opens the server's half of the write gate. A connection's own
	// allow_write is the other half, and both are required.
	AllowWrite bool
	// Version is reported to the client during initialize.
	Version string

	// Stdin, Stdout and Stderr default to the process's own streams. Stdout
	// carries the protocol and nothing else; Stderr carries diagnostics.
	Stdin  io.ReadCloser
	Stdout io.WriteCloser
	Stderr io.Writer

	// connector substitutes the connect sequence, so tests can drive the tools
	// against a fake driver.
	connector connector
}

// Server exposes a set of configured connections over MCP.
type Server struct {
	conns      []config.Connection
	byName     map[string]config.Connection
	allowWrite bool
	version    string
	pool       *pool
	red        *redactor
	log        *slog.Logger
	in         io.ReadCloser
	out        io.WriteCloser
}

// New builds a server over the given configuration. It fails when the
// allowlist names a connection the configuration does not define, so a typo in
// --connections is reported rather than silently exposing nothing.
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("no configuration given")
	}
	in, out := opts.Stdin, opts.Stdout
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	red := &redactor{}
	conns, err := selectConnections(opts.Config.Connections, opts.Connections)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]config.Connection, len(conns))
	for _, conn := range conns {
		byName[conn.Name] = conn
	}

	conn := opts.connector
	if conn == nil {
		conn = newLiveOpener(&recordingResolver{inner: secrets.NewResolver(), red: red})
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	return &Server{
		conns:      conns,
		byName:     byName,
		allowWrite: opts.AllowWrite,
		version:    version,
		pool:       newPool(conn),
		red:        red,
		log:        slog.New(slog.NewTextHandler(&redactWriter{w: stderr, red: red}, &slog.HandlerOptions{Level: slog.LevelInfo})),
		in:         in,
		out:        out,
	}, nil
}

// selectConnections applies the allowlist, preserving the configured order.
func selectConnections(all []config.Connection, allowed []string) ([]config.Connection, error) {
	if len(allowed) == 0 {
		return all, nil
	}
	byName := make(map[string]config.Connection, len(all))
	for _, conn := range all {
		byName[conn.Name] = conn
	}
	out := make([]config.Connection, 0, len(allowed))
	for _, name := range allowed {
		conn, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("--connections names %q, which is not in the config", name)
		}
		out = append(out, conn)
	}
	return out, nil
}

// Run serves MCP over the configured streams — stdin and stdout by default —
// until the client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.serve(ctx, &mcpsdk.IOTransport{Reader: s.in, Writer: s.out})
}

// serve registers the tools and serves one session over transport, closing
// every pooled session on the way out. Run and the tests share it, so a test
// exercises the same server a client talks to.
func (s *Server) serve(ctx context.Context, transport mcpsdk.Transport) error {
	srv := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "d9s", Title: "d9s databases", Version: s.version},
		&mcpsdk.ServerOptions{Logger: s.log, Instructions: instructions},
	)
	s.register(srv)

	names := make([]string, len(s.conns))
	for i, conn := range s.conns {
		names[i] = conn.Name
	}
	s.log.Info("serving", "connections", strings.Join(names, ","), "allow_write", s.allowWrite)

	defer func() {
		if err := s.pool.close(); err != nil {
			s.log.Warn("closing sessions", "error", err)
		}
	}()
	if err := srv.Run(ctx, transport); err != nil {
		return fmt.Errorf("serving mcp: %w", err)
	}
	return nil
}

// toolFunc is what a tool implements: typed arguments in, response text out.
type toolFunc[In any] func(ctx context.Context, in In) (string, error)

// guard adapts a toolFunc into an SDK handler. Registering every tool through
// it makes the cross-cutting guarantees properties of the server rather than
// of each handler: nothing leaves without passing the redactor and the byte
// cap, and every call is accounted for on stderr.
func guard[In any](s *Server, name string, fn toolFunc[In]) mcpsdk.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, any, error) {
		started := time.Now()
		text, err := fn(ctx, in)
		if err != nil {
			s.log.Error("tool failed", "tool", name, "elapsed", time.Since(started), "error", err)
			return nil, nil, errors.New(s.red.scrub(err.Error()))
		}
		text = s.red.scrub(text)
		if capped, cut := cutBytes(text, MaxBytes-noticeReserve); cut {
			text = capped + fmt.Sprintf("\nTruncated: the response reached the %d-byte cap.\n", MaxBytes)
		}
		s.log.Info("tool ok", "tool", name, "elapsed", time.Since(started), "bytes", len(text))
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}}}, nil, nil
	}
}

// usageText is printed by `d9s mcp -help`.
const usageText = `d9s mcp — serve the configured connections to an MCP client over stdio.

Usage:
  d9s mcp [flags]

Flags:
  -config path         configuration file (default %s)
  -connections a,b     expose only these connections; the rest do not exist
  -allow-write         permit destructive statements on connections that also
                       set allow_write: true (both are required)
  -help                print this help and exit

Register it with Claude Code:
  claude mcp add d9s -- d9s mcp --connections staging-pg
`

// Exit codes, matching the meanings the other d9s subcommands document: 0 for
// a clean stop, 1 for a failure, 2 for a bad command line or an unusable
// configuration.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// Main runs the `d9s mcp` subcommand and returns the process exit code. args
// are the arguments following the subcommand name; version is reported to the
// client during initialize.
func Main(args []string, version string) int {
	defaultConfigPath := config.DefaultPath()
	fs := flag.NewFlagSet("d9s mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", defaultConfigPath, "configuration file")
	list := fs.String("connections", "", "comma-separated connections to expose (default: all)")
	allowWrite := fs.Bool("allow-write", false, "permit destructive statements on connections that set allow_write")
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usageText, defaultConfigPath) }
	if err := fs.Parse(args); err != nil {
		return exitUsage // flag has already reported the problem
	}

	cfg, warns, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "d9s mcp:", err)
		return exitUsage
	}
	srv, err := New(Options{
		Config:      cfg,
		Connections: splitList(*list),
		AllowWrite:  *allowWrite,
		Version:     version,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "d9s mcp:", err)
		return exitUsage
	}
	for _, w := range warns {
		srv.log.Warn("config", "connection", w.Connection, "message", w.Message)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "d9s mcp:", err)
		return exitError
	}
	return exitOK
}

// splitList parses a comma-separated flag value, dropping empty entries so a
// trailing comma is not read as a connection named "".
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}
