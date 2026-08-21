package db

import (
	"reflect"
	"testing"

	"github.com/andreim/d9s/internal/config"
)

// render describes a token the way the tests spell it out: kind and text.
func render(t Token) string {
	kinds := map[TokenKind]string{
		TokenWord:   "word",
		TokenQuoted: "quoted",
		TokenString: "string",
		TokenPunct:  "punct",
	}
	return kinds[t.Kind] + ":" + t.Text
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		script string
		want   []string
	}{
		{
			name:   "words and punctuation",
			script: "SELECT id, name FROM users;",
			want: []string{
				"word:SELECT", "word:id", "punct:,", "word:name",
				"word:FROM", "word:users", "punct:;",
			},
		},
		{
			name:   "whitespace is dropped",
			script: "  SELECT\n\t1  ",
			want:   []string{"word:SELECT", "word:1"},
		},
		{
			name:   "a line comment is dropped whole",
			script: "SELECT 1 -- FROM users\nFROM t",
			want:   []string{"word:SELECT", "word:1", "word:FROM", "word:t"},
		},
		{
			name:   "a block comment is dropped whole",
			script: "SELECT /* FROM users */ 1",
			want:   []string{"word:SELECT", "word:1"},
		},
		{
			name:   "a string literal stays one token",
			script: "SELECT 'a; FROM b'",
			want:   []string{"word:SELECT", "string:'a; FROM b'"},
		},
		{
			name:   "a doubled quote does not end the string",
			script: "SELECT 'it''s'",
			want:   []string{"word:SELECT", "string:'it''s'"},
		},
		{
			name:   "a dollar-quoted string stays one token",
			script: "SELECT $tag$ x; $tag$",
			want:   []string{"word:SELECT", "string:$tag$ x; $tag$"},
		},
		{
			name:   "a quoted name loses its quotes",
			script: `SELECT "My Col" FROM t`,
			want:   []string{"word:SELECT", "quoted:My Col", "word:FROM", "word:t"},
		},
		{
			name:   "a doubled quote inside a name collapses",
			script: `SELECT "we""ird"`,
			want:   []string{"word:SELECT", `quoted:we"ird`},
		},
		{
			name:   "backticks quote names on clickhouse only",
			engine: config.ClickHouse,
			script: "SELECT `My Col`",
			want:   []string{"word:SELECT", "quoted:My Col"},
		},
		{
			name:   "a backtick is punctuation elsewhere",
			script: "SELECT `x`",
			want:   []string{"word:SELECT", "punct:`", "word:x", "punct:`"},
		},
		{
			name:   "a dotted name keeps its dot as punctuation",
			script: "FROM analytics.events",
			want:   []string{"word:FROM", "word:analytics", "punct:.", "word:events"},
		},
		{
			name:   "an unterminated string runs to the end",
			script: "SELECT 'oops",
			want:   []string{"word:SELECT", "string:'oops"},
		},
		{
			name:   "redis scripts are not lexed",
			engine: config.Redis,
			script: "GET user:1",
		},
		{
			name:   "an empty script has no tokens",
			script: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			if engine == "" {
				engine = config.Postgres
			}
			toks := Tokenize(engine, tt.script)
			var got []string
			for _, tok := range toks {
				got = append(got, render(tok))
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Tokenize(%q) = %#v, want %#v", tt.script, got, tt.want)
			}
			// Every token must point at the run of source it came from.
			prev := 0
			for _, tok := range toks {
				if tok.Start < prev || tok.End > len(tt.script) || tok.Start >= tok.End {
					t.Errorf("token %s has range [%d,%d) in a %d-byte script",
						render(tok), tok.Start, tok.End, len(tt.script))
				}
				prev = tok.End
			}
		})
	}
}

func TestTokenizeSpansSource(t *testing.T) {
	const script = `SELECT "a b", 'c' FROM t -- x`
	for _, tok := range Tokenize(config.Postgres, script) {
		span := script[tok.Start:tok.End]
		if tok.Kind == TokenQuoted {
			if want := `"` + tok.Text + `"`; span != want {
				t.Errorf("quoted token spans %q, want %q", span, want)
			}
			continue
		}
		if span != tok.Text {
			t.Errorf("token spans %q but holds %q", span, tok.Text)
		}
	}
}

func TestIsNameByte(t *testing.T) {
	tests := []struct {
		name string
		in   byte
		want bool
	}{
		{name: "letter", in: 'x', want: true},
		{name: "capital", in: 'X', want: true},
		{name: "digit", in: '7', want: true},
		{name: "underscore", in: '_', want: true},
		{name: "utf-8 lead byte", in: 0xc3, want: true},
		{name: "space", in: ' '},
		{name: "dot", in: '.'},
		{name: "semicolon", in: ';'},
		{name: "quote", in: '"'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNameByte(tt.in); got != tt.want {
				t.Errorf("IsNameByte(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
