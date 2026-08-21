package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
)

const (
	// schemaTimeout bounds a catalog query; failures are reported in the panel.
	schemaTimeout = 30 * time.Second
	// schemaMaxRows is the most rows listed at once; the list scrolls to keep
	// the selection visible.
	schemaMaxRows = 12
)

// schemaLevel is the drill-in depth of the schema panel.
type schemaLevel int

const (
	schemaTables schemaLevel = iota
	schemaColumns
)

// schemaModel is the state of the schema panel: the tables of the current
// database — key prefixes for Redis — and, one level in, the columns of a
// single table, or the keys under a single prefix.
type schemaModel struct {
	open     bool
	inflight bool // a catalog query is running on the session driver
	errMsg   string
	level    schemaLevel

	tables []db.Table
	table  string // the table whose columns are listed
	cols   []db.Column

	filtering bool // '/' filter entry is active
	filter    string
	tableSel  int
	colSel    int
}

// schemaRow is one rendered line of the panel, already narrowed by the filter.
type schemaRow struct {
	name   string
	detail string
}

type schemaTablesMsg struct {
	tables []db.Table
	err    error
}

type schemaColumnsMsg struct {
	table string
	cols  []db.Column
	err   error
}

// --- commands -------------------------------------------------------------

func listTablesCmd(driver db.Driver) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
		defer cancel()
		tables, err := driver.ListTables(ctx)
		if err != nil {
			return schemaTablesMsg{err: fmt.Errorf("listing tables: %w", err)}
		}
		return schemaTablesMsg{tables: tables}
	}
}

func listColumnsCmd(driver db.Driver, table string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
		defer cancel()
		cols, err := driver.ListColumns(ctx, table)
		if err != nil {
			return schemaColumnsMsg{table: table, err: fmt.Errorf("describing %s: %w", table, err)}
		}
		return schemaColumnsMsg{table: table, cols: cols}
	}
}

func (m *model) handleSchemaTables(msg schemaTablesMsg) {
	s := &m.query.schema
	s.inflight = false
	if !s.open {
		// The user closed the panel while the catalog query was running.
		return
	}
	if msg.err != nil {
		s.errMsg = msg.err.Error()
		return
	}
	s.errMsg = ""
	s.tables = msg.tables
	m.query.clampSchemaSelection()
}

func (m *model) handleSchemaColumns(msg schemaColumnsMsg) {
	s := &m.query.schema
	s.inflight = false
	if !s.open || s.table != msg.table {
		return
	}
	if msg.err != nil {
		s.errMsg = msg.err.Error()
		return
	}
	s.errMsg = ""
	s.cols = msg.cols
	m.query.clampSchemaSelection()
}

// --- opening, drilling and inserting --------------------------------------

// openSchema shows the panel and refreshes the table list. Catalog queries run
// on the session driver, which a run in flight is already using, so the panel
// refuses to open until that run finishes.
func (m *model) openSchema() tea.Cmd {
	q := &m.query
	if q.driver == nil {
		return nil
	}
	if q.running {
		m.status = "query running – ctrl+x to cancel first"
		return nil
	}
	s := &q.schema
	s.open = true
	s.level = schemaTables
	s.table = ""
	s.cols = nil
	s.filter = ""
	s.filtering = false
	s.errMsg = ""
	s.tableSel = 0
	if s.inflight {
		return nil
	}
	s.inflight = true
	return tea.Batch(m.spinner.Tick, listTablesCmd(q.driver))
}

func (q *queryModel) closeSchema() {
	s := &q.schema
	s.open = false
	s.level = schemaTables
	s.tables = nil
	s.table = ""
	s.cols = nil
	s.filter = ""
	s.filtering = false
	s.errMsg = ""
	s.tableSel, s.colSel = 0, 0
}

// drillSchema loads the columns of the highlighted table.
func (m *model) drillSchema() tea.Cmd {
	q := &m.query
	s := &q.schema
	if s.level == schemaColumns || s.inflight {
		return nil
	}
	rows := q.schemaRows()
	if s.tableSel >= len(rows) {
		return nil
	}
	s.level = schemaColumns
	s.table = rows[s.tableSel].name
	s.cols = nil
	s.colSel = 0
	s.filter = ""
	s.errMsg = ""
	s.inflight = true
	return tea.Batch(m.spinner.Tick, listColumnsCmd(q.driver, s.table))
}

// backSchema returns from the column list to the table list.
func (q *queryModel) backSchema() {
	s := &q.schema
	s.level = schemaTables
	s.table = ""
	s.cols = nil
	s.colSel = 0
	s.filter = ""
	s.filtering = false
	s.errMsg = ""
}

// insertSchemaTemplate closes the panel and puts a ready statement for the
// highlighted table in the editor. It never executes the statement.
func (m *model) insertSchemaTemplate() tea.Cmd {
	q := &m.query
	s := &q.schema
	table, cols := s.table, s.cols
	if s.level == schemaTables {
		rows := q.schemaRows()
		if s.tableSel >= len(rows) {
			return nil
		}
		table, cols = rows[s.tableSel].name, nil
	}
	stmt := schemaTemplate(q.engine, table, cols)
	if stmt == "" {
		return nil
	}
	q.closeSchema()
	q.ta.SetValue(stmt)
	q.focusResults = false
	m.status = "inserted from schema · ctrl+r to run"
	return q.ta.Focus()
}

// --- key handling ---------------------------------------------------------

// updateSchema handles keys while the panel is open: movement, enter to drill
// into a table, `i` to insert its statement, `/` to filter and esc to go back.
func (m *model) updateSchema(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	s := &q.schema
	if s.filtering {
		return m.updateSchemaFilter(msg)
	}
	switch msg.String() {
	case "esc", "s", "ctrl+s":
		if s.level == schemaColumns {
			q.backSchema()
			return nil
		}
		q.closeSchema()
	case "enter":
		return m.drillSchema()
	case "i":
		return m.insertSchemaTemplate()
	case "/":
		s.filtering = true
	case "j", "down", "ctrl+n":
		s.move(1, len(q.schemaRows()))
	case "k", "up", "ctrl+p":
		s.move(-1, len(q.schemaRows()))
	}
	return nil
}

// updateSchemaFilter handles keys while '/' filter entry is active: runes
// narrow the list, enter keeps the filter, esc drops it.
func (m *model) updateSchemaFilter(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	s := &q.schema
	switch msg.String() {
	case "esc":
		s.filtering = false
		s.setFilter("")
		return nil
	case "enter":
		s.filtering = false
		return nil
	case "backspace":
		if r := []rune(s.filter); len(r) > 0 {
			s.setFilter(string(r[:len(r)-1]))
		}
		return nil
	case "ctrl+u":
		s.setFilter("")
		return nil
	case "down", "ctrl+n":
		s.move(1, len(q.schemaRows()))
		return nil
	case "up", "ctrl+p":
		s.move(-1, len(q.schemaRows()))
		return nil
	}

	switch {
	case msg.Type == tea.KeyRunes && !msg.Alt:
		s.setFilter(s.filter + string(msg.Runes))
	case msg.Type == tea.KeySpace:
		s.setFilter(s.filter + " ")
	}
	return nil
}

// --- rows and selection ---------------------------------------------------

// schemaRows returns the rows of the current level, narrowed by the filter.
func (q *queryModel) schemaRows() []schemaRow {
	s := &q.schema
	var rows []schemaRow
	if s.level == schemaTables {
		for _, t := range s.tables {
			if matchesFilter(t.Name, s.filter) {
				rows = append(rows, schemaRow{name: t.Name, detail: t.Detail})
			}
		}
		return rows
	}
	for _, c := range s.cols {
		if matchesFilter(c.Name, s.filter) {
			rows = append(rows, schemaRow{name: c.Name, detail: columnDetail(q.engine, c)})
		}
	}
	return rows
}

// matchesFilter reports whether name contains filter, case-insensitively.
func matchesFilter(name, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), strings.ToLower(filter))
}

// columnDetail renders a column's type, nullability and extra detail. Redis
// keys have no nullability, so only their type and TTL are shown.
func columnDetail(engine config.EngineType, c db.Column) string {
	parts := make([]string, 0, 3)
	if c.Type != "" {
		parts = append(parts, c.Type)
	}
	if engine != config.Redis {
		if c.Nullable {
			parts = append(parts, "null")
		} else {
			parts = append(parts, "not null")
		}
	}
	if c.Detail != "" {
		parts = append(parts, c.Detail)
	}
	return strings.Join(parts, " · ")
}

func (s *schemaModel) selection() int {
	if s.level == schemaColumns {
		return s.colSel
	}
	return s.tableSel
}

func (s *schemaModel) setSelection(i int) {
	if s.level == schemaColumns {
		s.colSel = i
		return
	}
	s.tableSel = i
}

// move shifts the selection by delta, clamped to the n visible rows.
func (s *schemaModel) move(delta, n int) {
	sel := s.selection() + delta
	if sel > n-1 {
		sel = n - 1
	}
	if sel < 0 {
		sel = 0
	}
	s.setSelection(sel)
}

func (s *schemaModel) setFilter(filter string) {
	s.filter = filter
	s.setSelection(0)
}

// clampSchemaSelection keeps the selection inside the list after a reload.
func (q *queryModel) clampSchemaSelection() {
	if n := len(q.schemaRows()); q.schema.selection() >= n {
		q.schema.setSelection(max(0, n-1))
	}
}

// --- statement templates --------------------------------------------------

// schemaTemplate builds the statement `i` inserts for a table: a bounded
// SELECT for SQL engines, or a bounded SCAN for a Redis key prefix. With no
// columns known the SELECT lists them all with `*`.
func schemaTemplate(engine config.EngineType, table string, cols []db.Column) string {
	if table == "" {
		return ""
	}
	if engine == config.Redis {
		return "SCAN 0 MATCH " + redisPattern(table) + " COUNT 100"
	}
	list := "*"
	if len(cols) > 0 {
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			names = append(names, quoteIdent(engine, c.Name))
		}
		list = strings.Join(names, ", ")
	}
	return fmt.Sprintf("SELECT %s FROM %s LIMIT 100;", list, quoteTable(engine, table))
}

// quoteTable quotes a possibly schema-qualified table name part by part, so
// `analytics.events` stays two identifiers.
func quoteTable(engine config.EngineType, table string) string {
	schema, name, ok := strings.Cut(table, ".")
	if !ok {
		return quoteIdent(engine, table)
	}
	return quoteIdent(engine, schema) + "." + quoteIdent(engine, name)
}

// quoteIdent quotes one identifier when it is not a plain lowercase word.
func quoteIdent(engine config.EngineType, name string) string {
	if plainIdent(name) {
		return name
	}
	if engine == config.ClickHouse {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// plainIdent reports whether name can appear unquoted in a statement.
func plainIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// redisPattern renders the SCAN pattern for a key prefix, quoting it when the
// pattern contains characters the command splitter would treat specially.
func redisPattern(prefix string) string {
	p := prefix + "*"
	if strings.ContainsAny(p, " \t\"'\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
	}
	return p
}

// --- rendering ------------------------------------------------------------

// schemaView renders the panel; spin is the current spinner frame, shown while
// a catalog query is in flight.
func (q *queryModel) schemaView(width, height int, spin string) string {
	inner := min(width-10, maxStmtWidth)
	if inner < 24 {
		inner = 24
	}
	rows := min(schemaMaxRows, height-8)
	if rows < 1 {
		rows = 1
	}

	var b strings.Builder
	title := "Schema · " + q.dbName
	if q.schema.level == schemaColumns {
		title = "Schema · " + q.schema.table
	}
	if q.schema.inflight {
		title += " " + spin
	}
	b.WriteString(stSection.Render(clip(title, inner)) + "\n")
	if q.schema.filtering || q.schema.filter != "" {
		filter := q.schema.filter
		if filter == "" {
			filter = stDim.Render("type to filter")
		}
		b.WriteString(stDim.Render("/") + filter + "\n")
	}
	b.WriteString("\n" + q.schemaList(inner, rows))
	b.WriteString("\n" + stDim.Render(q.schemaHints()))
	return stHelpBox.Render(b.String())
}

func (q *queryModel) schemaList(width, rows int) string {
	s := &q.schema
	visible := q.schemaRows()
	tables, columns, _ := schemaLabels(q.engine)
	switch {
	case s.errMsg != "":
		return stErr.Render(clip(s.errMsg, width)) + "\n"
	case s.inflight && len(visible) == 0:
		return stDim.Render("loading...") + "\n"
	case len(visible) == 0 && s.filter != "":
		return stDim.Render(clip(fmt.Sprintf("nothing matches %q", s.filter), width)) + "\n"
	case len(visible) == 0 && s.level == schemaColumns:
		return stDim.Render("no "+columns) + "\n"
	case len(visible) == 0:
		return stDim.Render("no "+tables+" in this database") + "\n"
	}

	sel := s.selection()
	start := 0
	if sel >= rows {
		start = sel - rows + 1
	}
	end := min(start+rows, len(visible))

	nameW := 0
	for _, r := range visible[start:end] {
		if n := len([]rune(clip(r.name, width/2))); n > nameW {
			nameW = n
		}
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		r := visible[i]
		name := pad(clip(r.name, width/2), nameW)
		cursor := "  "
		if i == sel {
			cursor = stSelected.Render("> ")
			name = stSelected.Render(name)
		}
		line := cursor + name
		if r.detail != "" {
			line += " " + stDim.Render(clip(r.detail, width-nameW-3))
		}
		b.WriteString(line + "\n")
	}
	if n := len(visible) - end + start; n > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  %d more (%d total)", n, len(visible))) + "\n")
	}
	return b.String()
}

// schemaHints is the key legend for the current level, shown both in the panel
// and in the footer.
func (q *queryModel) schemaHints() string {
	if q.schema.filtering {
		return "type to filter · enter keep · esc drop · ↑/↓ select"
	}
	_, columns, insert := schemaLabels(q.engine)
	if q.schema.level == schemaColumns {
		return "j/k move · i insert " + insert + " · / filter · esc back"
	}
	return "j/k move · enter " + columns + " · i insert " + insert + " · / filter · esc close"
}

// schemaLabels adapts the panel's wording to the engine: Redis browses key
// prefixes and their keys rather than tables and columns.
func schemaLabels(engine config.EngineType) (tables, columns, insert string) {
	if engine == config.Redis {
		return "prefixes", "keys", "SCAN"
	}
	return "tables", "columns", "SELECT"
}
