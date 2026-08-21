package ui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
)

// fakeDriver serves canned catalog answers so the panel can be driven without
// a database.
type fakeDriver struct {
	tables    []db.Table
	cols      map[string][]db.Column
	tablesErr error
	colsErr   error
	asked     []string // tables named in ListColumns calls, in order
}

func (f *fakeDriver) Connect(context.Context, db.Target) error { return nil }

func (f *fakeDriver) ListDatabases(context.Context) ([]db.Database, error) { return nil, nil }

func (f *fakeDriver) ListTables(context.Context) ([]db.Table, error) {
	if f.tablesErr != nil {
		return nil, f.tablesErr
	}
	return f.tables, nil
}

func (f *fakeDriver) ListColumns(_ context.Context, table string) ([]db.Column, error) {
	f.asked = append(f.asked, table)
	if f.colsErr != nil {
		return nil, f.colsErr
	}
	return f.cols[table], nil
}

func (f *fakeDriver) Execute(_ context.Context, statement string) db.Result {
	return db.Result{Statement: statement, Affected: -1}
}

func (f *fakeDriver) Close() error { return nil }

func sampleDriver() *fakeDriver {
	return &fakeDriver{
		tables: []db.Table{
			{Name: "users", Detail: "~120 rows"},
			{Name: "user_sessions", Detail: "~4 rows"},
			{Name: "analytics.events", Detail: "~9000 rows"},
		},
		cols: map[string][]db.Column{
			"users": {
				{Name: "id", Type: "bigint", Detail: "default nextval('users_id_seq')"},
				{Name: "email", Type: "text", Nullable: true},
			},
			"analytics.events": {
				{Name: "at", Type: "timestamptz"},
			},
		},
	}
}

// newSchemaModel returns a model in the query view wired to drv, with the
// results pane focused so bare `s` opens the panel.
func newSchemaModel(t *testing.T, engine config.EngineType, drv db.Driver) *model {
	t.Helper()
	m := &model{view: viewQuery, activeConn: -1, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
	m.query.open(drv, engine, "prod-pg", "app")
	m.query.layout(80, 24)
	_ = m.query.ta.Focus() // the real flow focuses the editor when the view opens
	m.query.focusResults = true
	return m
}

// runCmd executes cmd, flattening tea.Batch, and returns every message it
// produced. Nil commands yield nothing.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, runCmd(c)...)
	}
	return out
}

// deliver runs cmd and feeds the resulting messages back into the model, the
// way the Bubble Tea event loop would.
func deliver(m *model, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		switch msg := msg.(type) {
		case schemaTablesMsg:
			m.handleSchemaTables(msg)
		case schemaColumnsMsg:
			m.handleSchemaColumns(msg)
		}
	}
}

// rowNames is the visible row list of the panel.
func rowNames(m *model) []string {
	var names []string
	for _, r := range m.query.schemaRows() {
		names = append(names, r.name)
	}
	return names
}

func TestSchemaTemplate(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		table  string
		cols   []db.Column
		want   string
	}{
		{
			name:   "plain table without columns",
			engine: config.Postgres,
			table:  "users",
			want:   "SELECT * FROM users LIMIT 100;",
		},
		{
			name:   "schema-qualified table keeps both parts unquoted",
			engine: config.Postgres,
			table:  "analytics.events",
			want:   "SELECT * FROM analytics.events LIMIT 100;",
		},
		{
			name:   "known columns are listed explicitly",
			engine: config.Postgres,
			table:  "users",
			cols:   []db.Column{{Name: "id"}, {Name: "email"}},
			want:   "SELECT id, email FROM users LIMIT 100;",
		},
		{
			name:   "mixed-case identifiers are quoted",
			engine: config.Postgres,
			table:  "public.Orders",
			cols:   []db.Column{{Name: "id"}, {Name: "totalAmount"}},
			want:   `SELECT id, "totalAmount" FROM public."Orders" LIMIT 100;`,
		},
		{
			name:   "embedded quote is escaped",
			engine: config.Postgres,
			table:  `we"ird`,
			want:   `SELECT * FROM "we""ird" LIMIT 100;`,
		},
		{
			name:   "clickhouse quotes with backticks",
			engine: config.ClickHouse,
			table:  "Hits",
			cols:   []db.Column{{Name: "EventDate"}, {Name: "url"}},
			want:   "SELECT `EventDate`, url FROM `Hits` LIMIT 100;",
		},
		{
			name:   "redis scans the prefix",
			engine: config.Redis,
			table:  "user",
			want:   "SCAN 0 MATCH user* COUNT 100",
		},
		{
			name:   "redis ignores columns",
			engine: config.Redis,
			table:  "session",
			cols:   []db.Column{{Name: "session:9", Type: "string"}},
			want:   "SCAN 0 MATCH session* COUNT 100",
		},
		{
			name:   "redis prefix with a space is quoted",
			engine: config.Redis,
			table:  "two words",
			want:   `SCAN 0 MATCH "two words*" COUNT 100`,
		},
		{
			name:   "no table yields no statement",
			engine: config.Postgres,
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := schemaTemplate(tt.engine, tt.table, tt.cols); got != tt.want {
				t.Errorf("schemaTemplate(%q, %q, %d cols) = %q, want %q", tt.engine, tt.table, len(tt.cols), got, tt.want)
			}
		})
	}
}

func TestSchemaFilter(t *testing.T) {
	tests := []struct {
		name   string
		keys   []string
		want   []string
		filter string
	}{
		{
			name: "no filter lists every table",
			want: []string{"users", "user_sessions", "analytics.events"},
		},
		{
			name:   "slash filters case-insensitively",
			keys:   []string{"/", "U", "S", "E", "R"},
			filter: "USER",
			want:   []string{"users", "user_sessions"},
		},
		{
			name:   "filter matches anywhere in the name",
			keys:   []string{"/", "e", "v", "e", "n"},
			filter: "even",
			want:   []string{"analytics.events"},
		},
		{
			name:   "backspace widens the filter",
			keys:   []string{"/", "u", "s", "e", "r", "_", "backspace"},
			filter: "user",
			want:   []string{"users", "user_sessions"},
		},
		{
			name:   "enter keeps the filter and leaves filter entry",
			keys:   []string{"/", "u", "s", "e", "r", "enter"},
			filter: "user",
			want:   []string{"users", "user_sessions"},
		},
		{
			name:   "esc drops the filter",
			keys:   []string{"/", "u", "s", "e", "r", "esc"},
			filter: "",
			want:   []string{"users", "user_sessions", "analytics.events"},
		},
		{
			name:   "ctrl+u clears the filter",
			keys:   []string{"/", "u", "s", "ctrl+u"},
			filter: "",
			want:   []string{"users", "user_sessions", "analytics.events"},
		},
		{
			name:   "no match lists nothing",
			keys:   []string{"/", "z", "z"},
			filter: "zz",
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSchemaModel(t, config.Postgres, sampleDriver())
			deliver(m, m.updateQuery(key("s")))
			for _, k := range tt.keys {
				m.updateQuery(key(k))
			}
			if m.query.schema.filter != tt.filter {
				t.Errorf("filter = %q, want %q", m.query.schema.filter, tt.filter)
			}
			if got := rowNames(m); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("visible rows = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSchemaSelectionMovement(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want int
	}{
		{name: "starts at the first table", want: 0},
		{name: "j moves down", keys: []string{"j"}, want: 1},
		{name: "down stops at the last row", keys: []string{"down", "down", "down", "down"}, want: 2},
		{name: "k stops at the first row", keys: []string{"k"}, want: 0},
		{name: "up walks back", keys: []string{"j", "j", "up"}, want: 1},
		{name: "filtering resets the selection", keys: []string{"j", "j", "/", "u"}, want: 0},
		{name: "arrows move during filter entry", keys: []string{"/", "u", "down"}, want: 1},
		{name: "clamped when the filter shrinks the list", keys: []string{"j", "j", "/", "e", "v"}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSchemaModel(t, config.Postgres, sampleDriver())
			deliver(m, m.updateQuery(key("s")))
			for _, k := range tt.keys {
				m.updateQuery(key(k))
			}
			if got := m.query.schema.selection(); got != tt.want {
				t.Errorf("selection = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSchemaDrillAndBack(t *testing.T) {
	drv := sampleDriver()
	m := newSchemaModel(t, config.Postgres, drv)
	deliver(m, m.updateQuery(key("s")))

	deliver(m, m.updateQuery(key("enter")))
	s := &m.query.schema
	if s.level != schemaColumns || s.table != "users" {
		t.Fatalf("after enter: level=%v table=%q, want columns/users", s.level, s.table)
	}
	if got, want := rowNames(m), []string{"id", "email"}; !reflect.DeepEqual(got, want) {
		t.Errorf("columns = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(drv.asked, []string{"users"}) {
		t.Errorf("ListColumns called for %#v, want [users]", drv.asked)
	}

	// Esc goes back one level, keeping the table selection.
	m.updateQuery(key("esc"))
	if s.level != schemaTables || !s.open {
		t.Fatalf("after esc: level=%v open=%v, want tables/open", s.level, s.open)
	}
	if got, want := rowNames(m), []string{"users", "user_sessions", "analytics.events"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tables = %#v, want %#v", got, want)
	}

	// A second esc closes the panel without touching the editor.
	m.updateQuery(key("esc"))
	if s.open {
		t.Error("panel still open after the second esc")
	}
	if got := m.query.ta.Value(); got != "" {
		t.Errorf("editor = %q, want it untouched", got)
	}
	if m.view != viewQuery {
		t.Errorf("view = %v, want the query view", m.view)
	}
}

func TestSchemaInsertTemplate(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		keys   []string
		want   string
	}{
		{
			name:   "table level inserts SELECT *",
			engine: config.Postgres,
			keys:   []string{"i"},
			want:   "SELECT * FROM users LIMIT 100;",
		},
		{
			name:   "second table",
			engine: config.Postgres,
			keys:   []string{"j", "i"},
			want:   "SELECT * FROM user_sessions LIMIT 100;",
		},
		{
			name:   "column level lists the columns",
			engine: config.Postgres,
			keys:   []string{"enter", "i"},
			want:   "SELECT id, email FROM users LIMIT 100;",
		},
		{
			name:   "filtered selection inserts the match",
			engine: config.Postgres,
			keys:   []string{"/", "e", "v", "e", "n", "enter", "i"},
			want:   "SELECT * FROM analytics.events LIMIT 100;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSchemaModel(t, tt.engine, sampleDriver())
			deliver(m, m.updateQuery(key("s")))
			for _, k := range tt.keys {
				deliver(m, m.updateQuery(key(k)))
			}
			if m.query.schema.open {
				t.Error("panel still open after inserting")
			}
			if got := m.query.ta.Value(); got != tt.want {
				t.Errorf("editor = %q, want %q", got, tt.want)
			}
			if m.query.running {
				t.Error("inserting a template started a run")
			}
		})
	}
}

func TestSchemaRedisRows(t *testing.T) {
	drv := &fakeDriver{
		tables: []db.Table{{Name: "session", Detail: "1 keys"}, {Name: "user", Detail: "2 keys"}},
		cols: map[string][]db.Column{
			"user": {
				{Name: "user:1", Type: "hash", Detail: "ttl 1m0s"},
				{Name: "user:2", Type: "string"},
			},
		},
	}
	m := newSchemaModel(t, config.Redis, drv)
	deliver(m, m.updateQuery(key("s")))
	if got, want := rowNames(m), []string{"session", "user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prefixes = %#v, want %#v", got, want)
	}

	m.updateQuery(key("j"))
	deliver(m, m.updateQuery(key("enter")))
	rows := m.query.schemaRows()
	if len(rows) != 2 {
		t.Fatalf("got %d keys, want 2", len(rows))
	}
	// Redis keys have no nullability to report.
	if want := "hash · ttl 1m0s"; rows[0].detail != want {
		t.Errorf("key detail = %q, want %q", rows[0].detail, want)
	}
	if strings.Contains(rows[1].detail, "null") {
		t.Errorf("key detail = %q, want no nullability", rows[1].detail)
	}

	deliver(m, m.updateQuery(key("i")))
	if got, want := m.query.ta.Value(), "SCAN 0 MATCH user* COUNT 100"; got != want {
		t.Errorf("editor = %q, want %q", got, want)
	}
}

func TestSchemaColumnDetail(t *testing.T) {
	tests := []struct {
		name   string
		engine config.EngineType
		col    db.Column
		want   string
	}{
		{
			name:   "not null with a default",
			engine: config.Postgres,
			col:    db.Column{Name: "id", Type: "bigint", Detail: "default 1"},
			want:   "bigint · not null · default 1",
		},
		{
			name:   "nullable column",
			engine: config.Postgres,
			col:    db.Column{Name: "email", Type: "text", Nullable: true},
			want:   "text · null",
		},
		{
			name:   "redis key keeps type and ttl only",
			engine: config.Redis,
			col:    db.Column{Name: "user:1", Type: "hash", Detail: "ttl 30s"},
			want:   "hash · ttl 30s",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := columnDetail(tt.engine, tt.col); got != tt.want {
				t.Errorf("columnDetail(%q, %#v) = %q, want %q", tt.engine, tt.col, got, tt.want)
			}
		})
	}
}

func TestSchemaOpenBindings(t *testing.T) {
	// A bare `s` is editor input while the editor has focus.
	m := newSchemaModel(t, config.Postgres, sampleDriver())
	m.query.focusResults = false
	if cmd := m.updateQuery(key("s")); cmd != nil {
		deliver(m, cmd)
	}
	if m.query.schema.open {
		t.Error("`s` opened the panel while the editor was focused")
	}
	if got := m.query.ta.Value(); got != "s" {
		t.Errorf("editor = %q, want the typed %q", got, "s")
	}

	// ctrl+s opens it from the editor.
	deliver(m, m.updateQuery(key("ctrl+s")))
	if !m.query.schema.open {
		t.Fatal("ctrl+s did not open the panel")
	}
	if got, want := rowNames(m), []string{"users", "user_sessions", "analytics.events"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tables = %#v, want %#v", got, want)
	}
	if m.query.schema.inflight {
		t.Error("inflight still set after the tables arrived")
	}
}

func TestSchemaRefusedWhileRunning(t *testing.T) {
	m := newSchemaModel(t, config.Postgres, sampleDriver())
	m.query.running = true
	if cmd := m.updateQuery(key("s")); cmd != nil {
		t.Error("the panel dispatched a catalog query while a run was in flight")
	}
	if m.query.schema.open {
		t.Error("the panel opened while a run was in flight")
	}
	if m.status == "" {
		t.Error("no status message explaining why the panel did not open")
	}
}

func TestSchemaLoadError(t *testing.T) {
	drv := sampleDriver()
	drv.tablesErr = errors.New("permission denied")
	m := newSchemaModel(t, config.Postgres, drv)
	deliver(m, m.updateQuery(key("s")))

	s := &m.query.schema
	if !s.open {
		t.Fatal("panel closed on a failed load")
	}
	if s.inflight {
		t.Error("inflight still set after the failure")
	}
	if !strings.Contains(s.errMsg, "permission denied") {
		t.Errorf("errMsg = %q, want it to mention the driver error", s.errMsg)
	}
	if out := m.query.schemaView(80, 24, ""); !strings.Contains(out, "permission denied") {
		t.Errorf("panel does not show the error:\n%s", out)
	}
}

func TestSchemaViewRenders(t *testing.T) {
	m := newSchemaModel(t, config.Postgres, sampleDriver())
	deliver(m, m.updateQuery(key("s")))

	out := m.query.schemaView(80, 24, "")
	for _, want := range []string{"Schema · app", "users", "~120 rows", "i insert SELECT"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel does not mention %q:\n%s", want, out)
		}
	}

	deliver(m, m.updateQuery(key("enter")))
	out = m.query.schemaView(80, 24, "")
	for _, want := range []string{"Schema · users", "email", "text · null"} {
		if !strings.Contains(out, want) {
			t.Errorf("column panel does not mention %q:\n%s", want, out)
		}
	}

	empty := newSchemaModel(t, config.Postgres, &fakeDriver{})
	deliver(empty, empty.updateQuery(key("s")))
	if out := empty.query.schemaView(80, 24, ""); !strings.Contains(out, "no tables in this database") {
		t.Errorf("empty panel does not explain itself:\n%s", out)
	}

	redis := newSchemaModel(t, config.Redis, &fakeDriver{})
	deliver(redis, redis.updateQuery(key("s")))
	if out := redis.query.schemaView(80, 24, ""); !strings.Contains(out, "no prefixes in this database") {
		t.Errorf("redis panel does not use redis wording:\n%s", out)
	}
}
