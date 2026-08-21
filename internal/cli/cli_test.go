package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testConfig is a configuration whose hosts are deliberately unreachable:
// port 1 refuses immediately, so a command that gets as far as connecting
// fails fast and locally. Both connections opt out of TLS, because a direct
// connection would otherwise default to require.
const testConfig = `connections:
  - name: alpha
    type: postgres
    host: 127.0.0.1
    port: 1
    user: postgres
    database: postgres
    connect_timeout: 2s
    tls:
      mode: disable
  - name: beta
    type: redis
    host: 127.0.0.1
    port: 1
    connect_timeout: 2s
    tls:
      mode: disable
`

// writeConfig puts a configuration in a temporary directory and returns its
// path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the test config: %v", err)
	}
	return path
}

// result is what one command run produced.
type result struct {
	code   int
	stdout string
	stderr string
}

// invoke runs one subcommand against buffers instead of the process streams.
// stdin is empty and not a terminal unless the caller says otherwise.
func invoke(name string, args []string, e env) result {
	var out, errOut bytes.Buffer
	if e.stdin == nil {
		e.stdin = strings.NewReader("")
	}
	e.stdout, e.stderr = &out, &errOut
	code := run(name, args, e)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func TestResolveFormatDefaultsToTheDestination(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		terminal bool
		want     Format
		wantErr  string
	}{
		{name: "a terminal reads the table", terminal: true, want: FormatTable},
		{name: "a pipe gets machine-readable rows", terminal: false, want: FormatJSONL},
		{name: "an explicit format wins on a terminal", value: "csv", terminal: true, want: FormatCSV},
		{name: "an explicit format wins in a pipe", value: "table", terminal: false, want: FormatTable},
		{name: "json", value: "json", want: FormatJSON},
		{name: "jsonl", value: "jsonl", want: FormatJSONL},
		{name: "an unknown format is a usage error", value: "yaml", wantErr: "unknown output format"},
		{name: "the format is not guessed", value: "CSV", wantErr: "unknown output format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFormat(tt.value, tt.terminal)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveFormat(%q) = %q, want an error mentioning %q", tt.value, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				if code := exitCode(err); code != ExitUsage {
					t.Errorf("exit code = %d, want %d", code, ExitUsage)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveFormat(%q): %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("resolveFormat(%q, terminal=%v) = %q, want %q", tt.value, tt.terminal, got, tt.want)
			}
		})
	}
}

func TestConnectionsListsWithoutConnecting(t *testing.T) {
	cfg := writeConfig(t, testConfig)

	tests := []struct {
		name     string
		args     []string
		terminal bool
		want     []string
		notWant  []string
	}{
		{
			name: "piped output is machine-readable without a flag",
			want: []string{`"name":"alpha"`, `"type":"postgres"`, `"name":"beta"`},
		},
		{
			name: "a terminal gets the aligned table", terminal: true,
			want:    []string{"name", "alpha", "beta", "---"},
			notWant: []string{`"name"`},
		},
		{
			name: "csv on request", args: []string{"-o", "csv"}, terminal: true,
			want: []string{"name,type,host,port,database,ssh,tls", "alpha,postgres,127.0.0.1,1,postgres,,disable"},
		},
		{
			name: "json on request", args: []string{"-o", "json"},
			want: []string{"[\n", `"name": "alpha"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := invoke("connections", append([]string{"-config", cfg}, tt.args...), env{terminal: tt.terminal})
			if got.code != ExitOK {
				t.Fatalf("exit code = %d, want 0 (stderr: %s)", got.code, got.stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("stdout is missing %q:\n%s", want, got.stdout)
				}
			}
			for _, unwanted := range tt.notWant {
				if strings.Contains(got.stdout, unwanted) {
					t.Errorf("stdout unexpectedly contains %q:\n%s", unwanted, got.stdout)
				}
			}
		})
	}
}

// TestPipedOutputCarriesDataOnly is the contract a pipeline depends on: every
// line of stdout parses as a row, and the notices live on stderr.
func TestPipedOutputCarriesDataOnly(t *testing.T) {
	cfg := writeConfig(t, testConfig)
	got := invoke("connections", []string{"-config", cfg}, env{})
	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", got.code, got.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines of stdout, want one per connection:\n%s", len(lines), got.stdout)
	}
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Errorf("stdout line %q is not a JSON object: %v", line, err)
		}
	}
}

func TestConnectionsSaysWhereTheEmptyConfigurationLives(t *testing.T) {
	cfg := writeConfig(t, "connections: []\n")
	got := invoke("connections", []string{"-config", cfg}, env{})
	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want 0", got.code)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing: there are no rows", got.stdout)
	}
	if !strings.Contains(got.stderr, cfg) {
		t.Errorf("stderr = %q, want it to name %s", got.stderr, cfg)
	}
}

func TestDestructiveStatementsAreRefusedWithoutWrite(t *testing.T) {
	cfg := writeConfig(t, testConfig)

	tests := []struct {
		name       string
		connection string
		sql        string
		write      bool
		wantCode   int
		wantErr    []string
	}{
		{
			name: "drop", connection: "alpha", sql: "DROP TABLE users",
			wantCode: ExitRefused, wantErr: []string{"DROP TABLE users", "--write"},
		},
		{
			name: "delete without a where clause", connection: "alpha", sql: "DELETE FROM users",
			wantCode: ExitRefused, wantErr: []string{"DELETE FROM users", "--write"},
		},
		{
			name: "one destructive statement stops the whole script", connection: "alpha",
			sql:      "SELECT 1; TRUNCATE users; SELECT 2",
			wantCode: ExitRefused, wantErr: []string{"TRUNCATE users", "--write"},
		},
		{
			name: "flushall on redis", connection: "beta", sql: "FLUSHALL",
			wantCode: ExitRefused, wantErr: []string{"FLUSHALL", "--write"},
		},
		{
			name: "a delete with a where clause is not destructive", connection: "alpha",
			sql: "DELETE FROM users WHERE id = 1", wantCode: ExitConnect,
		},
		{
			name: "a read is not destructive", connection: "alpha", sql: "SELECT 1",
			wantCode: ExitConnect,
		},
		{
			name: "--write lets the same statement through to the engine", connection: "alpha",
			sql: "DROP TABLE users", write: true, wantCode: ExitConnect,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"-config", cfg, tt.connection, tt.sql}
			if tt.write {
				args = append(args, "--write")
			}
			got := invoke("query", args, env{})
			if got.code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, tt.wantCode, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing: no statement produced rows", got.stdout)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("stderr is missing %q:\n%s", want, got.stderr)
				}
			}
		})
	}
}

func TestExitCodesDistinguishTheFailures(t *testing.T) {
	cfg := writeConfig(t, testConfig)
	missing := filepath.Join(t.TempDir(), "nope.sql")

	tests := []struct {
		name    string
		command string
		args    []string
		env     env
		want    int
		wantErr string
	}{
		{
			name: "an unknown connection is a usage error", command: "query",
			args: []string{"alfa", "SELECT 1"}, want: ExitUsage,
			wantErr: `connection "alfa" is not configured (configured: alpha, beta)`,
		},
		{
			name: "an unknown connection on a listing", command: "databases",
			args: []string{"nope"}, want: ExitUsage, wantErr: "alpha, beta",
		},
		{
			name: "an unknown flag", command: "connections",
			args: []string{"-nope"}, want: ExitUsage,
		},
		{
			name: "a flag the command does not take", command: "tables",
			args: []string{"-f", "x.sql", "alpha"}, want: ExitUsage,
		},
		{
			name: "too few arguments", command: "describe",
			args: []string{"alpha"}, want: ExitUsage, wantErr: "needs more arguments",
		},
		{
			name: "too many arguments", command: "connections",
			args: []string{"extra"}, want: ExitUsage, wantErr: "at most 0 argument",
		},
		{
			name: "an unreadable sql file", command: "query",
			args: []string{"alpha", "-f", missing}, want: ExitUsage, wantErr: "reading the SQL",
		},
		{
			name: "sql given twice", command: "query",
			args: []string{"alpha", "SELECT 1", "-f", missing}, want: ExitUsage,
			wantErr: "both as an argument and from -f",
		},
		{
			name: "no sql at all, with a terminal on stdin", command: "query",
			args: []string{"alpha"}, env: env{stdinTerminal: true}, want: ExitUsage,
			wantErr: "no SQL given",
		},
		{
			// A statement that starts with a dash needs the -- terminator,
			// which is also what makes this script reach the splitter at all.
			name: "an empty script", command: "query",
			args: []string{"alpha", "--", "-- just a comment"}, want: ExitUsage,
			wantErr: "no statements to run",
		},
		{
			name: "an unreachable database", command: "query",
			args: []string{"alpha", "SELECT 1"}, want: ExitConnect, wantErr: "127.0.0.1:1",
		},
		{
			name: "an unreachable database on a listing", command: "tables",
			args: []string{"alpha"}, want: ExitConnect, wantErr: "127.0.0.1:1",
		},
		{
			name: "a refused destructive statement", command: "query",
			args: []string{"alpha", "DROP TABLE t"}, want: ExitRefused, wantErr: "--write",
		},
		{
			name: "success", command: "connections", args: nil, want: ExitOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := invoke(tt.command, append([]string{"-config", cfg}, tt.args...), tt.env)
			if got.code != tt.want {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, tt.want, got.stderr)
			}
			if tt.wantErr != "" && !strings.Contains(got.stderr, tt.wantErr) {
				t.Errorf("stderr is missing %q:\n%s", tt.wantErr, got.stderr)
			}
		})
	}
}

// TestQueryReadsStdin covers the pipeline shape: SQL arrives on stdin and the
// statement is the one that reaches the engine.
func TestQueryReadsStdin(t *testing.T) {
	cfg := writeConfig(t, testConfig)
	got := invoke("query", []string{"-config", cfg, "alpha"},
		env{stdin: strings.NewReader("DROP TABLE piped\n")})
	if got.code != ExitRefused {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitRefused, got.stderr)
	}
	if !strings.Contains(got.stderr, "DROP TABLE piped") {
		t.Errorf("stderr is missing the piped statement:\n%s", got.stderr)
	}
}

// TestQueryReadsAFile covers -f, including that the file's statements are
// split the same way an argument's are.
func TestQueryReadsAFile(t *testing.T) {
	cfg := writeConfig(t, testConfig)
	sql := filepath.Join(t.TempDir(), "script.sql")
	if err := os.WriteFile(sql, []byte("SELECT 1;\nDROP TABLE from_a_file;\n"), 0o600); err != nil {
		t.Fatalf("writing the script: %v", err)
	}
	got := invoke("query", []string{"-config", cfg, "-f", sql, "alpha"}, env{})
	if got.code != ExitRefused {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitRefused, got.stderr)
	}
	if !strings.Contains(got.stderr, "DROP TABLE from_a_file") {
		t.Errorf("stderr is missing the file's statement:\n%s", got.stderr)
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	got := invoke("frobnicate", nil, env{})
	if got.code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", got.code, ExitUsage)
	}
	if !strings.Contains(got.stderr, "unknown command") {
		t.Errorf("stderr = %q, want it to report an unknown command", got.stderr)
	}
}

func TestHelpPrintsTheUsageAndExitsUsage(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			got := invoke(name, []string{"-help"}, env{})
			if got.code != ExitUsage {
				t.Fatalf("exit code = %d, want %d", got.code, ExitUsage)
			}
			if !strings.Contains(got.stderr, Synopsis(name)) {
				t.Errorf("help is missing the synopsis %q:\n%s", Synopsis(name), got.stderr)
			}
			if !strings.Contains(got.stderr, "Exit codes:") {
				t.Errorf("help is missing the exit codes:\n%s", got.stderr)
			}
		})
	}
}

func TestParseArgsAcceptsFlagsAroundThePositionals(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPos    []string
		wantFormat string
		wantWrite  bool
		wantErr    bool
	}{
		{
			name: "flags first", args: []string{"-o", "csv", "prod", "SELECT 1"},
			wantPos: []string{"prod", "SELECT 1"}, wantFormat: "csv",
		},
		{
			name: "flags last", args: []string{"prod", "SELECT 1", "-o", "csv"},
			wantPos: []string{"prod", "SELECT 1"}, wantFormat: "csv",
		},
		{
			name: "flags interleaved", args: []string{"prod", "-o", "csv", "SELECT 1", "--write"},
			wantPos: []string{"prod", "SELECT 1"}, wantFormat: "csv", wantWrite: true,
		},
		{
			name: "a double dash ends the flags", args: []string{"prod", "--", "-o", "csv"},
			wantPos: []string{"prod", "-o", "csv"},
		},
		{
			name: "no arguments at all", args: nil,
		},
		{
			name: "an unknown flag", args: []string{"-nope"}, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var format string
			var write bool
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(&bytes.Buffer{})
			fs.StringVar(&format, "o", "", "")
			fs.BoolVar(&write, "write", false, "")

			pos, err := parseArgs(fs, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseArgs(%q) = %q, want an error", tt.args, pos)
				}
				if code := exitCode(err); code != ExitUsage {
					t.Errorf("exit code = %d, want %d", code, ExitUsage)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", tt.args, err)
			}
			if strings.Join(pos, "|") != strings.Join(tt.wantPos, "|") {
				t.Errorf("positional = %q, want %q", pos, tt.wantPos)
			}
			if format != tt.wantFormat {
				t.Errorf("-o = %q, want %q", format, tt.wantFormat)
			}
			if write != tt.wantWrite {
				t.Errorf("--write = %v, want %v", write, tt.wantWrite)
			}
		})
	}
}

func TestABadConfigurationIsAUsageError(t *testing.T) {
	cfg := writeConfig(t, "connections:\n  - name: alpha\n    type: sqlite\n    host: x\n")
	got := invoke("connections", []string{"-config", cfg}, env{})
	if got.code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", got.code, ExitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "unknown type") {
		t.Errorf("stderr = %q, want it to name the bad type", got.stderr)
	}
}

func TestAPlaintextPasswordWarnsOnStderr(t *testing.T) {
	cfg := writeConfig(t, `connections:
  - name: alpha
    type: postgres
    host: 127.0.0.1
    user: postgres
    password: hunter2
`)
	got := invoke("connections", []string{"-config", cfg}, env{})
	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want 0", got.code)
	}
	if !strings.Contains(got.stderr, "plaintext") {
		t.Errorf("stderr = %q, want the plaintext-password warning", got.stderr)
	}
	if strings.Contains(got.stdout, "hunter2") {
		t.Errorf("stdout leaks the password:\n%s", got.stdout)
	}
}
