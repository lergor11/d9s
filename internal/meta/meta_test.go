package meta

import (
	"strings"
	"testing"
)

func TestIs(t *testing.T) {
	tests := []struct {
		stmt string
		want bool
	}{
		{`\dt`, true},
		{`  \l`, true},
		{`SELECT 1`, false},
		{`SELECT '\dt'`, false},
		{``, false},
	}
	for _, tt := range tests {
		if got := Is(tt.stmt); got != tt.want {
			t.Errorf("Is(%q) = %v, want %v", tt.stmt, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		stmt    string
		want    Command
		wantErr []string // substrings of the error; empty = success
	}{
		{name: "bare list", stmt: `\dt`, want: Command{Verb: "dt"}},
		{name: "describe with argument", stmt: `\d users`, want: Command{Verb: "d", Arg: "users"}},
		{name: "plus flag", stmt: `\d+ users`, want: Command{Verb: "d", Plus: true, Arg: "users"}},
		{
			name: "quoted argument keeps spaces and case",
			stmt: `\d+ "My Table"`,
			want: Command{Verb: "d", Plus: true, Arg: "My Table"},
		},
		{
			name: "doubled quotes escape a quote",
			stmt: `\d "a""b"`,
			want: Command{Verb: "d", Arg: `a"b`},
		},
		{name: "help", stmt: `\?`, want: Command{Verb: "?"}},
		{name: "quit", stmt: `\q`, want: Command{Verb: "q"}},
		{name: "surrounding blanks", stmt: `  \l  `, want: Command{Verb: "l"}},
		{
			name: "typo suggests the closest command",
			stmt: `\dtt`, wantErr: []string{`\dtt`, `\dt`},
		},
		{
			name: "unknown verb without a lookalike",
			stmt: `\xyzzy`, wantErr: []string{`\xyzzy`, "not a command"},
		},
		{
			name: "unterminated quote is a parse error",
			stmt: `\d "users`, wantErr: []string{"unterminated"},
		},
		{
			name: "a second argument is refused",
			stmt: `\d users orders`, wantErr: []string{"at most one argument", "orders"},
		},
		{name: "a lone backslash is incomplete", stmt: `\`, wantErr: []string{"incomplete"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.stmt)
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want an error", tt.stmt, got)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %q, want it to mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.stmt, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.stmt, got, tt.want)
			}
		})
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	rows := Help()
	if len(rows) != len(commands) {
		t.Fatalf("Help() has %d rows, want %d", len(rows), len(commands))
	}
	for i, c := range commands {
		if !strings.HasPrefix(rows[i][0], `\`+c.verb) {
			t.Errorf("row %d = %q, want it to start with \\%s", i, rows[i][0], c.verb)
		}
		if rows[i][1] == "" {
			t.Errorf(`\%s has no description`, c.verb)
		}
	}
}
