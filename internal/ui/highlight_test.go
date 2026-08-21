package ui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
)

// rowOf is the zero-based buffer row a byte offset falls on. The editor itself
// works from the line table it already has; a test can afford to count.
func rowOf(s string, offset int) int {
	return strings.Count(s[:min(max(offset, 0), len(s))], "\n")
}

// renderSpans describes spans the way the tests spell them out: kind and text.
func renderSpans(buf string, spans []highlightSpan) []string {
	names := map[spanKind]string{
		spanPlain:   "plain",
		spanKeyword: "keyword",
		spanString:  "string",
		spanNumber:  "number",
		spanComment: "comment",
	}
	var out []string
	for _, s := range spans {
		out = append(out, names[s.kind]+":"+buf[s.start:s.end])
	}
	return out
}

func TestHighlightSpans(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		buf    string
		want   []string
	}{
		{
			name: "keyword, string and comment each get their own colour",
			buf:  "SELECT 'a;b' FROM t -- note",
			want: []string{
				"keyword:SELECT", "plain: ", "string:'a;b'", "plain: ",
				"keyword:FROM", "plain: t ", "comment:-- note",
			},
		},
		{
			name: "numbers are distinct from identifiers",
			buf:  "WHERE id2 > 2.5",
			want: []string{"keyword:WHERE", "plain: id2 > ", "number:2.5"},
		},
		{
			name: "keywords are recognised whatever their case",
			buf:  "select 1 from t",
			want: []string{
				"keyword:select", "plain: ", "number:1", "plain: ",
				"keyword:from", "plain: t",
			},
		},
		{
			name: "a quoted name is an identifier, not a string",
			buf:  `SELECT "from" FROM t`,
			want: []string{
				"keyword:SELECT", `plain: "from" `, "keyword:FROM", "plain: t",
			},
		},
		{
			name: "a block comment keeps its colour across lines",
			buf:  "SELECT /* a\nb */ 1",
			want: []string{
				"keyword:SELECT", "plain: ", "comment:/* a\nb */", "plain: ", "number:1",
			},
		},
		{
			name: "a keyword inside a string stays a string",
			buf:  "SELECT 'FROM'",
			want: []string{"keyword:SELECT", "plain: ", "string:'FROM'"},
		},
		{
			name:   "redis colours the command apart from its arguments",
			engine: config.Redis,
			buf:    "GET user:1",
			want:   []string{"keyword:GET", "plain: user:1"},
		},
		{
			name:   "redis quoted arguments and numbers",
			engine: config.Redis,
			buf:    `SET k "two words" 42`,
			want: []string{
				"keyword:SET", "plain: k ", `string:"two words"`, "plain: ", "number:42",
			},
		},
		{
			name:   "a redis comment line",
			engine: config.Redis,
			buf:    "# note\nGET a",
			want:   []string{"comment:# note", "plain:\n", "keyword:GET", "plain: a"},
		},
		{
			name: "an empty buffer has nothing to colour",
			buf:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			if engine == "" {
				engine = config.Postgres
			}
			spans := spansIn(db.Tokenize(engine, tt.buf), 0, len(tt.buf))
			if got := renderSpans(tt.buf, spans); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("spans = %#v, want %#v", got, tt.want)
			}
			// Whatever the colours, the spans must tile the buffer exactly:
			// nothing dropped, nothing repeated, nothing reordered.
			var b strings.Builder
			at := 0
			for _, s := range spans {
				if s.start != at {
					t.Fatalf("span %#v starts at %d, want %d: the runs must not have gaps", s, s.start, at)
				}
				b.WriteString(tt.buf[s.start:s.end])
				at = s.end
			}
			if b.String() != tt.buf {
				t.Errorf("spans cover %q, want the whole buffer %q", b.String(), tt.buf)
			}
		})
	}
}

func TestHighlightSpansWindow(t *testing.T) {
	const buf = "SELECT 1;\nSELECT 'x';\nSELECT 3;"
	from := strings.Index(buf, "SELECT 'x'")
	to := from + len("SELECT 'x';")

	spans := spansIn(db.Tokenize(config.Postgres, buf), from, to)
	want := []string{"keyword:SELECT", "plain: ", "string:'x'", "plain:;"}
	if got := renderSpans(buf, spans); !reflect.DeepEqual(got, want) {
		t.Errorf("windowed spans = %#v, want %#v", got, want)
	}
	if spans[0].start != from || spans[len(spans)-1].end != to {
		t.Errorf("spans cover [%d,%d), want [%d,%d)", spans[0].start, spans[len(spans)-1].end, from, to)
	}
}

func TestHighlightSpansWindowInsideAString(t *testing.T) {
	// The window opens inside a string that started above it: the colour has to
	// come from lexing the whole buffer, not from the window alone.
	const buf = "SELECT 'a\nb' FROM t"
	from := strings.Index(buf, "b'")
	spans := spansIn(tokenizeWindow(config.Postgres, buf, from, len(buf)), from, len(buf))
	want := []string{"string:b'", "plain: ", "keyword:FROM", "plain: t"}
	if got := renderSpans(buf, spans); !reflect.DeepEqual(got, want) {
		t.Errorf("spans = %#v, want %#v", got, want)
	}
}

func TestGroupRuns(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		kinds  []spanKind
		cursor int
		want   []string // "text" or ">text<" for the cursor cell
	}{
		{
			name:   "neighbours of one kind become one run",
			text:   "abc",
			kinds:  []spanKind{spanKeyword, spanKeyword, spanKeyword},
			cursor: -1,
			want:   []string{"abc"},
		},
		{
			name:   "a change of kind starts a run",
			text:   "ab12",
			kinds:  []spanKind{spanKeyword, spanKeyword, spanNumber, spanNumber},
			cursor: -1,
			want:   []string{"ab", "12"},
		},
		{
			name:   "the cursor rune stands alone",
			text:   "abc",
			kinds:  []spanKind{spanKeyword, spanKeyword, spanKeyword},
			cursor: 1,
			want:   []string{"a", ">b<", "c"},
		},
		{
			name:   "the cursor on the first rune",
			text:   "abc",
			kinds:  []spanKind{spanPlain, spanPlain, spanPlain},
			cursor: 0,
			want:   []string{">a<", "bc"},
		},
		{
			name:   "no runes, no runs",
			cursor: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := groupRuns([]rune(tt.text), tt.kinds, tt.cursor)
			var got []string
			var joined strings.Builder
			for _, r := range runs {
				joined.WriteString(r.text)
				if r.cursor {
					got = append(got, ">"+r.text+"<")
					continue
				}
				got = append(got, r.text)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("runs = %#v, want %#v", got, tt.want)
			}
			if joined.String() != tt.text {
				t.Errorf("runs join to %q, want the row text %q", joined.String(), tt.text)
			}
		})
	}
}

func TestStatementRows(t *testing.T) {
	const script = "SELECT 1;\nSELECT 2,\n       3;\n\n-- tail\nSELECT 4"
	tests := []struct {
		name        string
		engine      config.EngineType
		buf         string
		cursor      int
		first, last int
		ok          bool
	}{
		{
			name: "the first statement", buf: script, cursor: 3,
			first: 0, last: 0, ok: true,
		},
		{
			name: "a statement spanning two rows", buf: script, cursor: strings.Index(script, "SELECT 2") + 3,
			first: 1, last: 2, ok: true,
		},
		{
			name: "a comment before the last statement is not part of it",
			buf:  script, cursor: len(script) - 1,
			first: 5, last: 5, ok: true,
		},
		{
			name: "the cursor just past a semicolon still marks what it closed",
			buf:  "SELECT 1;", cursor: 9,
			first: 0, last: 0, ok: true,
		},
		{
			// Past the semicolon the cursor is inside the statement that one
			// opened, so the marker moves on with it.
			name: "the cursor in the gap after a semicolon marks the statement it opens",
			buf:  script, cursor: strings.Index(script, "\n\n") + 1,
			first: 5, last: 5, ok: true,
		},
		{name: "an empty buffer marks nothing", buf: "", cursor: 0},
		{name: "a comment-only buffer marks nothing", buf: "-- just a note", cursor: 4},
		{
			name:   "redis marks the command line the cursor is on",
			engine: config.Redis, buf: "GET a\nSET b 1\nDEL c", cursor: 8,
			first: 1, last: 1, ok: true,
		},
		{
			name:   "a redis comment line marks nothing",
			engine: config.Redis, buf: "GET a\n# note\nDEL c", cursor: 9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			if engine == "" {
				engine = config.Postgres
			}
			start, end, ok := statementBounds(db.Tokenize(engine, tt.buf), engine, tt.buf, tt.cursor)
			first, last := rowOf(tt.buf, start), rowOf(tt.buf, max(end-1, 0))
			if ok != tt.ok {
				t.Fatalf("marked = %v, want %v", ok, tt.ok)
			}
			if ok && (first != tt.first || last != tt.last) {
				t.Errorf("rows = %d..%d, want %d..%d", first, last, tt.first, tt.last)
			}
		})
	}
}

// editorRows returns the editor's rendered rows with their styling stripped.
func editorRows(m *model) []string {
	return strings.Split(stripANSI(m.query.editorView()), "\n")
}

// rowBody is the text of a rendered row, without the gutter, the line number,
// and the padding that squares the editor off.
func rowBody(row string, lines int) string {
	prefix := gutterWidth + len(strconv.Itoa(lines)) + 2
	r := []rune(row)
	if len(r) < prefix {
		return ""
	}
	return strings.TrimRight(string(r[prefix:]), " ")
}

func TestEditorViewKeepsTheBufferText(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		buf    string
	}{
		{name: "the spec's statement", buf: "SELECT 'a;b' FROM t -- note"},
		{name: "several statements", buf: "SELECT 1;\nSELECT 2;\nSELECT 3;"},
		{name: "punctuation and operators", buf: "SELECT a->>'k', b::int FROM t WHERE x <> 1;"},
		{name: "an indented continuation", buf: "SELECT id\n  FROM users\n WHERE id = 1;"},
		{name: "unicode", buf: "SELECT 'héllo → wörld' AS grüße;"},
		{name: "a redis script", engine: config.Redis, buf: "# note\nGET user:1\nSET k \"two words\" 42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			if engine == "" {
				engine = config.Postgres
			}
			m := newCompletionModel(t, engine, completionDriver())
			setEditor(&m.query, tt.buf)

			lines := strings.Split(tt.buf, "\n")
			rows := editorRows(m)
			if len(rows) != m.query.ta.Height() {
				t.Fatalf("rendered %d rows, want the editor's %d", len(rows), m.query.ta.Height())
			}
			for i, line := range lines {
				if got := rowBody(rows[i], len(lines)); got != line {
					t.Errorf("row %d = %q, want the buffer line %q", i+1, got, line)
				}
			}
			if got := m.query.ta.Value(); got != tt.buf {
				t.Errorf("buffer = %q, want it untouched by rendering", got)
			}
		})
	}
}

func TestEditorViewHighlightSurvivesEditing(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	setEditor(&m.query, "SELECT id FROM users|;")
	row, col := m.query.cursorRowCol()
	if row != 0 || col != 20 {
		t.Fatalf("cursor at %d,%d, want 0,20", row, col)
	}

	// Typing in the middle of the statement leaves the cursor where the user
	// put it, one column further along, and the text is what was typed.
	press(m, "2")
	if got, want := m.query.ta.Value(), "SELECT id FROM users2;"; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	row, col = m.query.cursorRowCol()
	if row != 0 || col != 21 {
		t.Errorf("cursor at %d,%d after typing, want 0,21", row, col)
	}
	if got := rowBody(editorRows(m)[0], 1); got != "SELECT id FROM users2;" {
		t.Errorf("row = %q, want the edited line", got)
	}

	// The colours follow the edit: what was a keyword is one no longer.
	const edited = "SELECT id FROM users2;"
	spans := spansIn(db.Tokenize(config.Postgres, edited), 0, len(edited))
	if got := renderSpans(edited, spans); got[0] != "keyword:SELECT" {
		t.Errorf("spans = %#v, want SELECT still a keyword", got)
	}
	press(m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	if got, want := m.query.ta.Value(), "SELECT id FROM;"; got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestEditorViewMarksTheCursorStatement(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	setEditor(&m.query, "SELECT 1;\nSELECT 2,\n       3;\nSELECT 4;")

	// The cursor lands on the second statement, which spans two rows.
	m.query.ta.CursorUp()
	m.query.ta.CursorUp()
	m.query.ta.SetCursor(2)

	var marked []int
	for i, row := range editorRows(m) {
		if strings.HasPrefix(row, "┃") {
			marked = append(marked, i)
		}
	}
	if want := []int{1, 2}; !reflect.DeepEqual(marked, want) {
		t.Errorf("marked rows = %v, want %v", marked, want)
	}
}

func TestEditorViewScrollsToTheCursor(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, "SELECT "+strconv.Itoa(i)+";")
	}
	setEditor(&m.query, strings.Join(lines, "\n"))

	height := m.query.ta.Height()
	rows := editorRows(m)
	if len(rows) != height {
		t.Fatalf("rendered %d rows, want %d", len(rows), height)
	}
	// The cursor sits on the last line, so the window ends there.
	if last := rowBody(rows[height-1], len(lines)); last != "SELECT 30;" {
		t.Errorf("last visible row = %q, want the cursor's line", last)
	}

	// Back to the top, and the window follows.
	for range lines {
		m.query.ta.CursorUp()
	}
	if first := rowBody(editorRows(m)[0], len(lines)); first != "SELECT 1;" {
		t.Errorf("first visible row = %q, want the cursor's line", first)
	}

	// A line wider than the editor scrolls sideways instead of wrapping.
	wide := "SELECT " + strings.Repeat("x", 400) + " FROM t;"
	setEditor(&m.query, wide)
	rows = editorRows(m)
	if len(rows) != height {
		t.Fatalf("a long line rendered %d rows, want %d: it must not wrap", len(rows), height)
	}
	if body := rowBody(rows[0], 1); !strings.HasSuffix(body, "FROM t;") {
		t.Errorf("row = %q, want the end of the line the cursor is on", body)
	}
}

func TestEditorViewPlaceholder(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	rows := editorRows(m)
	if len(rows) != m.query.ta.Height() {
		t.Fatalf("rendered %d rows, want %d", len(rows), m.query.ta.Height())
	}
	if got, want := rowBody(rows[0], 1), placeholderFor(config.Postgres); got != want {
		t.Errorf("placeholder row = %q, want %q", got, want)
	}
	if got := m.query.ta.Value(); got != "" {
		t.Errorf("buffer = %q, want the placeholder to leave it empty", got)
	}
}

func TestSyntaxStylesAreDistinct(t *testing.T) {
	kinds := map[spanKind]string{
		spanKeyword: "keyword",
		spanString:  "string",
		spanNumber:  "number",
		spanComment: "comment",
	}
	seen := map[string]string{}
	for kind, name := range kinds {
		colour := syntaxStyle(kind).GetForeground()
		if colour == (lipgloss.NoColor{}) {
			t.Errorf("%s has no colour of its own", name)
			continue
		}
		key := colourKey(colour)
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s share the colour %s", name, other, key)
		}
		seen[key] = name
	}
	// Identifiers keep the terminal's foreground, which is what makes them
	// readable whichever way round the terminal's colours are.
	if syntaxStyle(spanPlain).GetForeground() != (lipgloss.NoColor{}) {
		t.Error("plain text sets a colour, want the terminal's own foreground")
	}
	// The palette must stay mid-tone: the extremes of the 256-colour cube are
	// the ones that vanish into a light or a dark background.
	for kind, name := range kinds {
		key := colourKey(syntaxStyle(kind).GetForeground())
		n, err := strconv.Atoi(key)
		if err != nil {
			continue // a named or hex colour, nothing to bound
		}
		if n < 17 || n > 250 {
			t.Errorf("%s uses colour %d, want a mid-tone one that reads on light and dark", name, n)
		}
	}
}

// colourKey renders a lipgloss colour as the value it was declared with.
func colourKey(c lipgloss.TerminalColor) string { return fmt.Sprint(c) }

// benchBuffer is a script of n identical statements, each holding a keyword, a
// name, a string, a number and a comment.
func benchBuffer(n int) string {
	var b strings.Builder
	for range n {
		b.WriteString("SELECT id, email, 'note' FROM users WHERE id = 42 -- why\n")
	}
	return b.String()
}

// BenchmarkEditorView measures one frame of the editor. The cost must stay flat
// as the buffer grows, because only the visible window is lexed and styled.
func BenchmarkEditorView(b *testing.B) {
	for _, lines := range []int{20, 100, 1000, 10000} {
		b.Run(strconv.Itoa(lines)+"lines", func(b *testing.B) {
			m := &model{view: viewQuery, activeConn: -1}
			m.query.open(&fakeDriver{}, config.Postgres, "conn", "app")
			m.query.layout(100, 30)
			m.query.ta.SetValue(benchBuffer(lines))
			b.ReportAllocs()
			for b.Loop() {
				_ = m.query.editorView()
			}
		})
	}
}
