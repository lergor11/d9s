package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/andreim/d9s/internal/cli"
)

func TestEveryCommandIsRegistered(t *testing.T) {
	for _, name := range cli.Names() {
		if _, ok := subcommands[name]; !ok {
			t.Errorf("subcommand %q is implemented but not dispatched", name)
		}
	}
	for name := range subcommands {
		if cli.Synopsis(name) == "" {
			t.Errorf("subcommand %q is dispatched but has no synopsis", name)
		}
	}
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantCode   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name: "help goes to stdout so it can be paged", command: "help", wantCode: cli.ExitOK,
			wantStdout: []string{"Usage:", "Commands (each takes -help of its own):",
				"d9s query <connection> [sql]", "Exit codes:"},
		},
		{
			name: "an unknown command lists the real ones", command: "databsaes",
			wantCode:   cli.ExitUsage,
			wantStderr: append([]string{`unknown command "databsaes"`}, cli.Names()...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := dispatch(tt.command, nil, "/tmp/config.yaml", &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d", code, tt.wantCode)
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(out.String(), want) {
					t.Errorf("stdout is missing %q:\n%s", want, out.String())
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("stderr is missing %q:\n%s", want, errOut.String())
				}
			}
		})
	}
}

// TestHelpNamesTheConfigPath checks the one piece of the help that is not a
// constant: the user's own configuration path.
func TestHelpNamesTheConfigPath(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := dispatch("help", nil, "/home/someone/.config/d9s/config.yaml", &out, &errOut); code != cli.ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "/home/someone/.config/d9s/config.yaml") {
		t.Errorf("the help does not name the configuration path:\n%s", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", errOut.String())
	}
}
