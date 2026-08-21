package db

import (
	"reflect"
	"testing"

	"github.com/lergor11/d9s/internal/config"
)

// render describes a token the way the tests spell it out: kind and text.
func render(t Token) string {
	kinds := map[TokenKind]string{
		TokenWord:    "word",
		TokenQuoted:  "quoted",
		TokenString:  "string",
		TokenNumber:  "number",
		TokenComment: "comment",
		TokenCommand: "command",
		TokenPunct:   "punct",
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
			want:   []string{"word:SELECT", "number:1"},
		},
		{
			name:   "a line comment stays one token",
			script: "SELECT 1 -- FROM users\nFROM t",
			want: []string{
				"word:SELECT", "number:1", "comment:-- FROM users\n", "word:FROM", "word:t",
			},
		},
		{
			name:   "a block comment stays one token",
			script: "SELECT /* FROM users */ 1",
			want:   []string{"word:SELECT", "comment:/* FROM users */", "number:1"},
		},
		{
			name:   "numbers keep their fraction and exponent",
			script: "SELECT 1, 2.5, 3e-9, 0x1f",
			want: []string{
				"word:SELECT", "number:1", "punct:,", "number:2.5", "punct:,",
				"number:3e-9", "punct:,", "number:0x1f",
			},
		},
		{
			name:   "a name that merely holds digits is still a name",
			script: "SELECT t1.id2",
			want:   []string{"word:SELECT", "word:t1", "punct:.", "word:id2"},
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
			name:   "redis names the command apart from its arguments",
			engine: config.Redis,
			script: "GET user:1",
			want:   []string{"command:GET", "word:user:1"},
		},
		{
			name:   "redis lexes each line on its own",
			engine: config.Redis,
			script: "GET a\nSET b 12",
			want:   []string{"command:GET", "word:a", "command:SET", "word:b", "number:12"},
		},
		{
			name:   "redis keeps a quoted argument whole",
			engine: config.Redis,
			script: `SET k "two words"`,
			want:   []string{"command:SET", "word:k", `string:"two words"`},
		},
		{
			name:   "redis honors the escape inside a quoted argument",
			engine: config.Redis,
			script: `SET k "a\" b" 2`,
			want:   []string{"command:SET", "word:k", `string:"a\" b"`, "number:2"},
		},
		{
			name:   "a redis comment line is a comment",
			engine: config.Redis,
			script: "# note\nGET a",
			want:   []string{"comment:# note", "command:GET", "word:a"},
		},
		{
			name:   "a hash inside a redis argument is not a comment",
			engine: config.Redis,
			script: "GET #tag",
			want:   []string{"command:GET", "word:#tag"},
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
