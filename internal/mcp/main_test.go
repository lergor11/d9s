package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig puts a config file in a temporary directory and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	return path
}

// TestMainRejectsBadInvocations covers the paths that return before the server
// starts serving; the serving path itself is covered by TestStdioProtocol.
func TestMainRejectsBadInvocations(t *testing.T) {
	valid := writeConfig(t, "connections:\n  - name: pg\n    type: postgres\n    host: 10.0.0.1\n")

	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "an unknown flag",
			args: []string{"-nope"},
			want: exitUsage,
		},
		{
			name: "a config that does not parse",
			args: []string{"-config", writeConfig(t, "connections: [oops\n")},
			want: exitUsage,
		},
		{
			name: "an allowlist naming a connection that is not configured",
			args: []string{"-config", valid, "-connections", "typo-pg"},
			want: exitUsage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Main(tt.args, "test"); got != tt.want {
				t.Errorf("Main(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

func TestSplitList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{in: "", want: nil},
		{in: "a", want: []string{"a"}},
		{in: "a,b", want: []string{"a", "b"}},
		{in: " a , b ", want: []string{"a", "b"}},
		{in: "a,,b,", want: []string{"a", "b"}},
		{in: ",", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := splitList(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitList(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitList(%q) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}
