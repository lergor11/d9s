package ui

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// compKey turns a key name into the KeyMsg the terminal would produce. It
// covers the keys completion binds and hands everything else to key().
func compKey(name string) tea.KeyMsg {
	switch name {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	}
	return key(name)
}

// completionDriver is the schema the completion tests complete against.
func completionDriver() *fakeDriver {
	return &fakeDriver{
		tables: []db.Table{
			{Name: "users"},
			{Name: "user_roles"},
			{Name: "orders"},
			{Name: "analytics.events"},
		},
		cols: map[string][]db.Column{
			"users":            {{Name: "id"}, {Name: "email"}, {Name: "created_at"}},
			"user_roles":       {{Name: "user_id"}, {Name: "role"}},
			"orders":           {{Name: "id"}, {Name: "user_id"}, {Name: "total"}},
			"analytics.events": {{Name: "at"}, {Name: "name"}},
		},
	}
}

// redisDriverFixture answers the key scans the Redis completion tests make.
// ListColumns is the driver's bounded SCAN of a prefix.
func redisDriverFixture() *fakeDriver {
	return &fakeDriver{
		tables: []db.Table{{Name: "user"}, {Name: "session"}},
		cols: map[string][]db.Column{
			// Keyed by the prefix the driver scans, the way ListColumns
			// answers `SCAN MATCH <prefix>*` for Redis.
			"user":  {{Name: "user:1"}, {Name: "user:2"}},
			"user:": {{Name: "user:1"}, {Name: "user:2"}},
			"":      {{Name: "user:1"}, {Name: "user:2"}, {Name: "session:a"}},
		},
	}
}

// newCompletionModel returns a model in the query view with the editor focused,
// the way opening a database leaves it.
func newCompletionModel(t *testing.T, engine config.EngineType, drv db.Driver) *model {
	t.Helper()
	m := &model{view: viewQuery, activeConn: -1, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
	m.query.open(drv, engine, "prod-pg", "app")
	m.query.layout(80, 24)
	_ = m.query.ta.Focus()
	return m
}

// setEditor fills the editor, putting the cursor where the marker '|' stands;
// without a marker the cursor lands at the end of the (single-line) text.
func setEditor(q *queryModel, text string) {
	col := strings.Index(text, "|")
	clean := strings.Replace(text, "|", "", 1)
	q.ta.SetValue(clean)
	if col >= 0 {
		q.ta.SetCursor(utf8.RuneCountInString(clean[:col]))
	}
}

// deliverCompletion feeds a completion's messages back into the model, then
// follows the background schema loads it starts through to the popup they open.
func deliverCompletion(m *model, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		if msg, ok := msg.(completionSchemaMsg); ok {
			deliverCompletion(m, m.handleCompletionSchema(msg))
		}
	}
}

// press sends keys to the query view, settling each one's background work.
func press(m *model, names ...string) {
	for _, name := range names {
		deliverCompletion(m, m.updateQuery(compKey(name)))
	}
}

// cursorOf strips the marker '|' from buf and returns the text with the byte
// offset the marker stood at.
func cursorOf(buf string) (string, int) {
	if i := strings.Index(buf, "|"); i >= 0 {
		return strings.Replace(buf, "|", "", 1), i
	}
	return buf, len(buf)
}

func TestCompletionRequestAt(t *testing.T) {
	tests := []struct {
		name      string
		engine    config.EngineType
		buf       string // '|' marks the cursor
		kind      completionKind
		word      string
		qualifier string
		tables    []string
	}{
		{
			name: "tables after FROM",
			buf:  "SELECT * FROM |",
			kind: completeTables,
		},
		{
			name: "tables after FROM with a prefix typed",
			buf:  "SELECT * FROM us|",
			kind: completeTables,
			word: "us",
		},
		{
			name: "tables after JOIN",
			buf:  "SELECT * FROM users u JOIN or|",
			kind: completeTables,
			word: "or",
		},
		{
			name: "tables after a comma continuing the FROM list",
			buf:  "SELECT * FROM users, |",
			kind: completeTables,
		},
		{
			name: "tables after INSERT INTO",
			buf:  "INSERT INTO |",
			kind: completeTables,
		},
		{
			name: "tables after UPDATE",
			buf:  "UPDATE ord|",
			kind: completeTables,
			word: "ord",
		},
		{
			name: "tables after CREATE TABLE",
			buf:  "CREATE TABLE |",
			kind: completeTables,
		},
		{
			name:   "columns after SELECT",
			buf:    "SELECT | FROM users",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name:   "columns after WHERE",
			buf:    "SELECT * FROM users WHERE em|",
			kind:   completeColumns,
			word:   "em",
			tables: []string{"users"},
		},
		{
			name:   "columns after ON across both joined tables",
			buf:    "SELECT * FROM users u JOIN orders o ON |",
			kind:   completeColumns,
			tables: []string{"users", "orders"},
		},
		{
			name:   "columns after GROUP BY",
			buf:    "SELECT count(*) FROM users GROUP BY |",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name:   "columns after ORDER BY",
			buf:    "SELECT * FROM users ORDER BY |",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name:   "columns after HAVING",
			buf:    "SELECT count(*) FROM users GROUP BY id HAVING |",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name:   "columns after SET",
			buf:    "UPDATE users SET |",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name:   "columns inside an INSERT column list",
			buf:    "INSERT INTO users (|",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name:   "columns inside a function call",
			buf:    "SELECT count(cr|) FROM users",
			kind:   completeColumns,
			word:   "cr",
			tables: []string{"users"},
		},
		{
			name:      "alias-qualified columns",
			buf:       "SELECT u.| FROM users u",
			kind:      completeColumns,
			qualifier: "u",
			tables:    []string{"users"},
		},
		{
			name:      "alias declared with AS",
			buf:       "SELECT * FROM users AS u WHERE u.em|",
			kind:      completeColumns,
			word:      "em",
			qualifier: "u",
			tables:    []string{"users"},
		},
		{
			name:      "table name used as its own qualifier",
			buf:       "SELECT users.| FROM users",
			kind:      completeColumns,
			qualifier: "users",
			tables:    []string{"users"},
		},
		{
			name:      "a qualifier in a table position names a schema",
			buf:       "SELECT * FROM analytics.|",
			kind:      completeTables,
			qualifier: "analytics",
		},
		{
			name:      "an unknown qualifier still asks for that table's columns",
			buf:       "SELECT zz.| FROM users",
			kind:      completeColumns,
			qualifier: "zz",
			tables:    []string{"zz"},
		},
		{
			name: "keywords at the start of a statement",
			buf:  "SEL|",
			kind: completeKeywords,
			word: "SEL",
		},
		{
			name: "keywords where a table alias would go",
			buf:  "SELECT * FROM users |",
			kind: completeKeywords,
		},
		{
			name: "keywords when no table is named yet",
			buf:  "SELECT |",
			kind: completeKeywords,
		},
		{
			name:   "only the statement holding the cursor counts",
			buf:    "SELECT * FROM orders; SELECT | FROM users",
			kind:   completeColumns,
			tables: []string{"users"},
		},
		{
			name: "a semicolon inside a string does not split the statement",
			buf:  "SELECT ';' FROM users WHERE |",
			kind: completeColumns,

			tables: []string{"users"},
		},
		{
			name: "a keyword inside a comment is ignored",
			buf:  "SELECT * FROM users -- WHERE\n|",
			kind: completeKeywords,
		},
		{
			name:   "quoted table name",
			engine: config.ClickHouse,
			buf:    "SELECT * FROM `My Table` WHERE |",
			kind:   completeColumns,
			tables: []string{"My Table"},
		},
		{
			name:   "redis command at the start of a line",
			engine: config.Redis,
			buf:    "GE|",
			kind:   completeRedisCommands,
			word:   "GE",
		},
		{
			name:   "redis command on a later line",
			engine: config.Redis,
			buf:    "GET a\nSCA|",
			kind:   completeRedisCommands,
			word:   "SCA",
		},
		{
			name:   "redis key after a key-taking command",
			engine: config.Redis,
			buf:    "GET user:|",
			kind:   completeRedisKeys,
			word:   "user:",
		},
		{
			name:   "redis key with nothing typed yet",
			engine: config.Redis,
			buf:    "HGETALL |",
			kind:   completeRedisKeys,
		},
		{
			name:   "redis variadic command takes keys everywhere",
			engine: config.Redis,
			buf:    "DEL user:1 user:|",
			kind:   completeRedisKeys,
			word:   "user:",
		},
		{
			name:   "redis value argument completes nothing",
			engine: config.Redis,
			buf:    "SET user:1 va|",
			kind:   completeNone,
			word:   "va",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			if engine == "" {
				engine = config.Postgres
			}
			buf, cursor := cursorOf(tt.buf)
			got := completionRequestAt(engine, buf, cursor)
			if got.kind != tt.kind {
				t.Errorf("kind = %v, want %v", got.kind, tt.kind)
			}
			if got.word != tt.word {
				t.Errorf("word = %q, want %q", got.word, tt.word)
			}
			if got.qualifier != tt.qualifier {
				t.Errorf("qualifier = %q, want %q", got.qualifier, tt.qualifier)
			}
			if !reflect.DeepEqual(got.tables, tt.tables) {
				t.Errorf("tables = %#v, want %#v", got.tables, tt.tables)
			}
		})
	}
}

func TestRankCandidates(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		word  string
		want  []string
	}{
		{
			name:  "everything matches an empty word, alphabetically",
			names: []string{"users", "orders"},
			want:  []string{"orders", "users"},
		},
		{
			name:  "prefix matches come before substring matches",
			names: []string{"my_user", "users", "user_roles"},
			word:  "use",
			want:  []string{"user_roles", "users", "my_user"},
		},
		{
			name:  "matching ignores case",
			names: []string{"Email", "id"},
			word:  "em",
			want:  []string{"Email"},
		},
		{
			name:  "duplicates are dropped",
			names: []string{"id", "id", "email"},
			want:  []string{"email", "id"},
		},
		{
			name:  "nothing matches",
			names: []string{"users", "orders"},
			word:  "zz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rankCandidates(tt.names, tt.word); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rankCandidates(%#v, %q) = %#v, want %#v", tt.names, tt.word, got, tt.want)
			}
		})
	}
}

func TestSharedPrefix(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		word  string
		want  string
	}{
		{
			name:  "candidates share more than was typed",
			items: []string{"user_roles", "users"},
			word:  "us",
			want:  "user",
		},
		{
			name:  "nothing to add when the shared part is already typed",
			items: []string{"user_roles", "users"},
			word:  "user",
		},
		{
			name:  "a single candidate is inserted whole elsewhere",
			items: []string{"users"},
			word:  "us",
		},
		{
			name:  "substring matches share no prefix",
			items: []string{"users", "my_user"},
			word:  "us",
		},
		{
			name:  "the shared part keeps the first candidate's case",
			items: []string{"GETDEL", "GETEX", "GETRANGE"},
			word:  "ge",
			want:  "GET",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sharedPrefix(tt.items, tt.word); got != tt.want {
				t.Errorf("sharedPrefix(%#v, %q) = %q, want %q", tt.items, tt.word, got, tt.want)
			}
		})
	}
}

func TestCompletionInsertsAndPopups(t *testing.T) {
	tests := []struct {
		name  string
		buf   string // '|' marks the cursor
		want  string // editor after Tab
		items []string
		open  bool
	}{
		{
			name:  "tables are offered after FROM",
			buf:   "SELECT * FROM |",
			want:  "SELECT * FROM ",
			open:  true,
			items: []string{"analytics.events", "orders", "user_roles", "users"},
		},
		{
			name:  "a prefix narrows the tables and the shared part is inserted",
			buf:   "SELECT * FROM us|",
			want:  "SELECT * FROM user",
			open:  true,
			items: []string{"user_roles", "users"},
		},
		{
			name: "a unique match is inserted without a popup",
			buf:  "SELECT * FROM ord|",
			want: "SELECT * FROM orders",
		},
		{
			name:  "columns of the queried table are offered",
			buf:   "SELECT | FROM users",
			want:  "SELECT  FROM users",
			open:  true,
			items: []string{"created_at", "email", "id"},
		},
		{
			name:  "columns come from every table in the statement",
			buf:   "SELECT * FROM users u JOIN orders o ON |",
			want:  "SELECT * FROM users u JOIN orders o ON ",
			open:  true,
			items: []string{"created_at", "email", "id", "total", "user_id"},
		},
		{
			name:  "an alias resolves to its table",
			buf:   "SELECT u.| FROM users u",
			want:  "SELECT u. FROM users u",
			open:  true,
			items: []string{"created_at", "email", "id"},
		},
		{
			name: "an alias-qualified unique match is inserted",
			buf:  "SELECT u.em| FROM users u",
			want: "SELECT u.email FROM users u",
		},
		{
			name: "an unknown qualifier offers nothing and leaves the buffer alone",
			buf:  "SELECT zz.| FROM users u",
			want: "SELECT zz. FROM users u",
		},
		{
			name: "a keyword is completed when no schema context applies",
			buf:  "SEL|",
			want: "SELECT",
		},
		{
			name: "keywords follow the case being typed",
			buf:  "sel|",
			want: "select",
		},
		{
			name: "a qualifier naming a schema offers its tables",
			buf:  "SELECT * FROM analytics.|",
			want: "SELECT * FROM analytics.events",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newCompletionModel(t, config.Postgres, completionDriver())
			setEditor(&m.query, tt.buf)
			press(m, "tab")

			if got := m.query.ta.Value(); got != tt.want {
				t.Errorf("editor = %q, want %q", got, tt.want)
			}
			if m.query.comp.open != tt.open {
				t.Fatalf("popup open = %v, want %v", m.query.comp.open, tt.open)
			}
			if tt.open && !reflect.DeepEqual(m.query.comp.items, tt.items) {
				t.Errorf("candidates = %#v, want %#v", m.query.comp.items, tt.items)
			}
			if m.query.running {
				t.Error("completing started a run")
			}
		})
	}
}

func TestCompletionPopupNavigation(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string
		open bool
	}{
		{
			name: "enter inserts the first candidate",
			keys: []string{"enter"},
			want: "SELECT * FROM user_roles",
		},
		{
			name: "tab moves to the second candidate",
			keys: []string{"tab", "enter"},
			want: "SELECT * FROM users",
		},
		{
			name: "arrows move the selection",
			keys: []string{"down", "up", "down", "enter"},
			want: "SELECT * FROM users",
		},
		{
			name: "the selection wraps around",
			keys: []string{"up", "enter"},
			want: "SELECT * FROM users",
		},
		{
			name: "esc closes the popup and keeps what was typed",
			keys: []string{"esc"},
			want: "SELECT * FROM user",
		},
		{
			name: "typing narrows the list",
			keys: []string{"s"},
			want: "SELECT * FROM users",
			open: true,
		},
		{
			name: "typing until nothing matches closes the popup",
			keys: []string{"z"},
			want: "SELECT * FROM userz",
		},
		{
			name: "backspace widens the list again",
			keys: []string{"s", "backspace"},
			want: "SELECT * FROM user",
			open: true,
		},
		{
			name: "a space ends the name and closes the popup",
			keys: []string{"space"},
			want: "SELECT * FROM user ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newCompletionModel(t, config.Postgres, completionDriver())
			setEditor(&m.query, "SELECT * FROM user")
			press(m, "tab")
			if !m.query.comp.open {
				t.Fatalf("no popup for the ambiguous prefix; editor = %q", m.query.ta.Value())
			}
			press(m, tt.keys...)

			if got := m.query.ta.Value(); got != tt.want {
				t.Errorf("editor = %q, want %q", got, tt.want)
			}
			if m.query.comp.open != tt.open {
				t.Errorf("popup open = %v, want %v", m.query.comp.open, tt.open)
			}
			if !m.query.editorFocused() {
				t.Error("the editor lost focus")
			}
		})
	}
}

func TestCompletionColdCacheOpensPopupWhenNamesArrive(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	setEditor(&m.query, "SELECT * FROM us")

	// The first Tab cannot answer yet: the catalog query runs in the
	// background and the popup says so.
	cmd := m.updateQuery(compKey("tab"))
	if cmd == nil {
		t.Fatal("Tab on a cold cache dispatched no catalog query")
	}
	if !m.query.comp.open || !m.query.comp.pending {
		t.Fatalf("popup open=%v pending=%v, want both true", m.query.comp.open, m.query.comp.pending)
	}
	if m.query.comp.hint == "" {
		t.Error("no loading hint while the schema loads")
	}
	if out := m.query.view(""); !strings.Contains(out, "loading schema") {
		t.Errorf("the loading hint is not on screen:\n%s", out)
	}
	if got := m.query.ta.Value(); got != "SELECT * FROM us" {
		t.Errorf("editor = %q, want it untouched while loading", got)
	}

	// Once the names arrive the popup opens by itself.
	deliverCompletion(m, cmd)
	if !m.query.comp.open || m.query.comp.pending {
		t.Fatalf("popup open=%v pending=%v after the names arrived, want open and settled",
			m.query.comp.open, m.query.comp.pending)
	}
	if want := []string{"user_roles", "users"}; !reflect.DeepEqual(m.query.comp.items, want) {
		t.Errorf("candidates = %#v, want %#v", m.query.comp.items, want)
	}
	if got := m.query.ta.Value(); got != "SELECT * FROM user" {
		t.Errorf("editor = %q, want the shared prefix inserted", got)
	}
}

func TestCompletionCachesCatalogQueries(t *testing.T) {
	drv := completionDriver()
	m := newCompletionModel(t, config.Postgres, drv)

	setEditor(&m.query, "SELECT | FROM users")
	press(m, "tab")
	press(m, "esc")
	setEditor(&m.query, "SELECT id, | FROM users")
	press(m, "tab")

	if !reflect.DeepEqual(drv.asked, []string{"users"}) {
		t.Errorf("ListColumns called for %#v, want one call for users", drv.asked)
	}
}

func TestCompletionRefreshPicksUpANewTable(t *testing.T) {
	drv := completionDriver()
	m := newCompletionModel(t, config.Postgres, drv)
	setEditor(&m.query, "SELECT * FROM inv")
	press(m, "tab")
	if got := m.query.ta.Value(); got != "SELECT * FROM inv" {
		t.Fatalf("editor = %q, want it untouched with no matching table", got)
	}

	drv.tables = append(drv.tables, db.Table{Name: "invoices"})
	press(m, "ctrl+g")
	if m.status == "" {
		t.Error("refreshing said nothing in the status line")
	}
	press(m, "tab")
	if got, want := m.query.ta.Value(), "SELECT * FROM invoices"; got != want {
		t.Errorf("editor = %q, want %q", got, want)
	}
}

func TestCompletionRedis(t *testing.T) {
	t.Run("command names", func(t *testing.T) {
		m := newCompletionModel(t, config.Redis, redisDriverFixture())
		setEditor(&m.query, "GE")
		press(m, "tab")

		if got, want := m.query.ta.Value(), "GET"; got != want {
			t.Errorf("editor = %q, want the shared prefix %q", got, want)
		}
		if !m.query.comp.open {
			t.Fatal("no popup for the GE prefix")
		}
		for _, want := range []string{"GET", "GETDEL", "GETEX", "GETRANGE"} {
			if !slicesContain(m.query.comp.items, want) {
				t.Errorf("%q is not offered; candidates = %#v", want, m.query.comp.items)
			}
		}
	})

	t.Run("keys after a key-taking command", func(t *testing.T) {
		drv := redisDriverFixture()
		m := newCompletionModel(t, config.Redis, drv)
		setEditor(&m.query, "GET user:")
		press(m, "tab")

		if !m.query.comp.open {
			t.Fatalf("no popup; editor = %q", m.query.ta.Value())
		}
		if want := []string{"user:1", "user:2"}; !reflect.DeepEqual(m.query.comp.items, want) {
			t.Errorf("candidates = %#v, want %#v", m.query.comp.items, want)
		}
		// Keys come from the driver's bounded scan of the typed prefix, which
		// is what ListColumns runs for Redis; the table listing is untouched.
		if !reflect.DeepEqual(drv.asked, []string{"user:"}) {
			t.Errorf("scanned %#v, want one scan of the typed prefix", drv.asked)
		}

		// Accepting one replaces the whole typed key, colon included.
		press(m, "enter")
		if got, want := m.query.ta.Value(), "GET user:1"; got != want {
			t.Errorf("editor = %q, want %q", got, want)
		}
	})

	t.Run("keys share a prefix through their punctuation", func(t *testing.T) {
		m := newCompletionModel(t, config.Redis, redisDriverFixture())
		setEditor(&m.query, "GET user")
		press(m, "tab")

		// The colon is part of the key, so it comes along in the shared prefix.
		if got, want := m.query.ta.Value(), "GET user:"; got != want {
			t.Errorf("editor = %q, want %q", got, want)
		}
		if want := []string{"user:1", "user:2"}; !reflect.DeepEqual(m.query.comp.items, want) {
			t.Fatalf("candidates = %#v, want %#v", m.query.comp.items, want)
		}

		// Typing narrows across the punctuation instead of closing the popup.
		press(m, "2")
		if !m.query.comp.open {
			t.Fatalf("the popup closed while narrowing; editor = %q", m.query.ta.Value())
		}
		if want := []string{"user:2"}; !reflect.DeepEqual(m.query.comp.items, want) {
			t.Errorf("candidates = %#v, want %#v", m.query.comp.items, want)
		}
		press(m, "enter")
		if got, want := m.query.ta.Value(), "GET user:2"; got != want {
			t.Errorf("editor = %q, want %q", got, want)
		}
	})

	t.Run("a wider scan already in the cache is reused", func(t *testing.T) {
		drv := redisDriverFixture()
		m := newCompletionModel(t, config.Redis, drv)
		setEditor(&m.query, "GET ")
		press(m, "tab")
		press(m, "esc")
		setEditor(&m.query, "GET user:")
		press(m, "tab")

		if want := []string{"user:1", "user:2"}; !reflect.DeepEqual(m.query.comp.items, want) {
			t.Errorf("candidates = %#v, want %#v", m.query.comp.items, want)
		}
		if !reflect.DeepEqual(drv.asked, []string{""}) {
			t.Errorf("scanned %#v, want only the first, wider scan", drv.asked)
		}
	})

	t.Run("a value argument completes nothing", func(t *testing.T) {
		m := newCompletionModel(t, config.Redis, redisDriverFixture())
		setEditor(&m.query, "SET user:1 va")
		press(m, "tab")

		if m.query.comp.open {
			t.Error("a popup opened where no name can be completed")
		}
		if got, want := m.query.ta.Value(), "SET user:1 va"; got != want {
			t.Errorf("editor = %q, want %q", got, want)
		}
	})
}

func TestCompletionFocusBindings(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	setEditor(&m.query, "SELECT * FROM ord")

	// Tab completes in the editor instead of moving the focus.
	press(m, "tab")
	if m.query.focusResults {
		t.Error("tab moved the focus away from the editor")
	}
	if got, want := m.query.ta.Value(), "SELECT * FROM orders"; got != want {
		t.Errorf("editor = %q, want %q", got, want)
	}

	// Ctrl+J is the focus toggle now, from either side.
	press(m, "ctrl+j")
	if !m.query.focusResults {
		t.Fatal("ctrl+j did not move the focus to the results")
	}
	press(m, "ctrl+j")
	if m.query.focusResults {
		t.Fatal("ctrl+j did not move the focus back to the editor")
	}

	// Tab still toggles the focus from the results side.
	press(m, "ctrl+j", "tab")
	if m.query.focusResults {
		t.Error("tab did not move the focus back to the editor from the results")
	}
}

func TestCompletionRefusedWhileRunning(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	setEditor(&m.query, "SELECT * FROM us")
	m.query.running = true

	press(m, "tab")
	if m.query.comp.open {
		t.Error("a popup opened while the session driver was busy running a query")
	}
	if m.status == "" {
		t.Error("no status message explaining why nothing was completed")
	}
	if got := m.query.ta.Value(); got != "SELECT * FROM us" {
		t.Errorf("editor = %q, want it untouched", got)
	}
}

func TestCompletionHintsAndHelp(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	m.width, m.height = 80, 24

	footer := m.footerView()
	if !strings.Contains(footer, "tab complete") || !strings.Contains(footer, "ctrl+j results") {
		t.Errorf("the editor footer does not advertise the new bindings:\n%s", footer)
	}

	// With the popup open the footer explains the popup instead.
	setEditor(&m.query, "SELECT * FROM user")
	press(m, "tab")
	if got := m.footerView(); !strings.Contains(got, "enter insert") {
		t.Errorf("the footer does not explain the popup:\n%s", got)
	}
	press(m, "esc")

	// The results side keeps tab as its focus toggle.
	press(m, "ctrl+j")
	if got := m.footerView(); !strings.Contains(got, "tab editor") {
		t.Errorf("the results footer lost its tab hint:\n%s", got)
	}

	help := m.helpView()
	for _, want := range []string{"complete the name at the cursor", "ctrl+g", "ctrl+j"} {
		if !strings.Contains(help, want) {
			t.Errorf("the help overlay does not mention %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "tab            toggle editor/results focus") {
		t.Errorf("the help overlay still binds tab to the focus toggle:\n%s", help)
	}
}

func TestCompletionViewRenders(t *testing.T) {
	m := newCompletionModel(t, config.Postgres, completionDriver())
	setEditor(&m.query, "SELECT * FROM user")
	press(m, "tab")

	out := m.query.view("")
	for _, want := range []string{"user_roles", "users", "enter insert"} {
		if !strings.Contains(out, want) {
			t.Errorf("the popup does not show %q:\n%s", want, out)
		}
	}
	// The popup lies over the results, so the editor keeps its own height.
	if got, want := strings.Count(out, "\n"), strings.Count(m.query.view(""), "\n"); got != want {
		t.Errorf("view height changed with the popup open: %d lines, want %d", got, want)
	}
}

// slicesContain reports whether items holds want.
func slicesContain(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
