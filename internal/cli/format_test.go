package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/andreim/d9s/internal/db"
)

func TestRenderTableAligns(t *testing.T) {
	res := db.Result{
		Columns: []string{"id", "name"},
		Rows:    [][]string{{"1", "alpha"}, {"22", "b"}},
	}
	var buf bytes.Buffer
	if err := renderTable(&buf, res, 0); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	want := "id | name\n" +
		"---+------\n" +
		"1  | alpha\n" +
		"22 | b\n"
	if buf.String() != want {
		t.Errorf("table =\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestRenderTableShrinksToTheTerminal(t *testing.T) {
	long := strings.Repeat("x", 60)
	res := db.Result{Columns: []string{"a", "b"}, Rows: [][]string{{long, long}}}

	tests := []struct {
		name  string
		width int
		want  int // widest line, in runes
	}{
		{name: "unknown width caps each column", width: 0, want: 2*maxCellWidth + gapWidth},
		{name: "a wide terminal still caps each column", width: 200, want: 2*maxCellWidth + gapWidth},
		// The columns shrink in step, so the row lands on the widest size that
		// still fits rather than exactly on the terminal width.
		{name: "a narrow terminal shrinks the columns", width: 40, want: 39},
		{name: "columns never shrink past the floor", width: 4, want: 2*minCellWidth + gapWidth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderTable(&buf, res, tt.width); err != nil {
				t.Fatalf("renderTable: %v", err)
			}
			widest := 0
			for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
				if n := len([]rune(line)); n > widest {
					widest = n
				}
			}
			if widest != tt.want {
				t.Errorf("widest line = %d runes, want %d:\n%s", widest, tt.want, buf.String())
			}
			if !strings.Contains(buf.String(), "…") {
				t.Error("a clipped cell should be marked with an ellipsis")
			}
		})
	}
}

func TestWriteKeepsDiagnosticsOffStdout(t *testing.T) {
	res := db.Result{
		Columns:  []string{"x"},
		Rows:     [][]string{{"1"}},
		Affected: -1,
		Duration: 3 * time.Millisecond,
	}

	tests := []struct {
		name       string
		format     Format
		wantStdout string
		wantStderr string
	}{
		{
			name: "the table labels in place", format: FormatTable,
			wantStdout: "[1] SELECT 1\nx\n-\n1\n", wantStderr: "",
		},
		{
			name: "jsonl sends the label to stderr", format: FormatJSONL,
			wantStdout: "{\"x\":\"1\"}\n", wantStderr: "[1] SELECT 1\n",
		},
		{
			name: "csv sends the label to stderr", format: FormatCSV,
			wantStdout: "x\n1\n", wantStderr: "[1] SELECT 1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			e := env{stdout: &out, stderr: &errOut}
			if err := e.write(tt.format, res, "[1] SELECT 1"); err != nil {
				t.Fatalf("write: %v", err)
			}
			if out.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", out.String(), tt.wantStdout)
			}
			if errOut.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}

func TestWriteReportsAStatementWithNoResultSet(t *testing.T) {
	res := db.Result{Affected: 2}

	tests := []struct {
		name       string
		format     Format
		wantStdout string
	}{
		{name: "the table says how many rows changed", format: FormatTable, wantStdout: "2 row(s) affected\n"},
		{name: "jsonl writes nothing at all", format: FormatJSONL},
		{name: "csv writes nothing at all", format: FormatCSV},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			e := env{stdout: &out, stderr: &errOut}
			if err := e.write(tt.format, res, ""); err != nil {
				t.Fatalf("write: %v", err)
			}
			if out.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", out.String(), tt.wantStdout)
			}
		})
	}
}

func TestClipCollapsesAndElides(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{name: "short enough", in: "abc", w: 8, want: "abc"},
		{name: "exactly the limit", in: "abcd", w: 4, want: "abcd"},
		{name: "elided", in: "abcdef", w: 4, want: "abc…"},
		{name: "a single column of room", in: "abcdef", w: 1, want: "…"},
		{name: "newlines become spaces", in: "SELECT 1\nFROM t", w: 20, want: "SELECT 1 FROM t"},
		{name: "no limit", in: "abcdef", w: 0, want: "abcdef"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clip(tt.in, tt.w); got != tt.want {
				t.Errorf("clip(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
			}
		})
	}
}
