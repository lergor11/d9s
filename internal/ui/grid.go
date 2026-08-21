package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/db"
	"github.com/andreim/d9s/internal/export"
)

const (
	// gridHeaderLines is how many lines a table puts above its first row: the
	// statement title, the column headers and the rule under them.
	gridHeaderLines = 3
	// gridColGap is the width of the " │ " between two columns.
	gridColGap = 3
	// nullCell is how every driver renders a SQL NULL. It sorts after real
	// values and never counts as a number.
	nullCell = "NULL"
)

// sortNone means the rows keep the order the engine returned them in.
const sortNone = -1

// filterAllColumns means the filter matches a row through any of its columns.
const filterAllColumns = -1

// gridModel is how the focused result is being read: which cell is selected,
// how the rows are ordered, and what narrows them. It applies to the result the
// results pane is focused on and resets when the focus moves to another one.
type gridModel struct {
	row, col int // selected cell, in displayed rows and real column indexes
	colLeft  int // first column drawn, so a wide table can scroll sideways

	sortCol  int // sortNone, or the column the rows are ordered by
	sortDesc bool

	filter    string
	filterCol int  // filterAllColumns, or the only column the filter looks at
	filtering bool // filter entry is active

	rows   [][]string // the rows on show: filtered, then sorted
	loaded int        // rows the result holds, before filtering
}

// reset returns the grid to a freshly opened result.
func (g *gridModel) reset() {
	*g = gridModel{sortCol: sortNone, filterCol: filterAllColumns}
}

// --- rows: filtering and sorting --------------------------------------------

// rebuildGrid recomputes the rows on show from the focused result. The result
// itself is never reordered or trimmed: the grid holds its own slice of the
// same rows.
func (q *queryModel) rebuildGrid() {
	g := &q.grid
	res, _, ok := q.focusedResult()
	if !ok || len(res.Columns) == 0 {
		g.rows, g.loaded = nil, 0
		g.row, g.col, g.colLeft = 0, 0, 0
		return
	}
	g.loaded = len(res.Rows)
	rows := filterRows(res.Rows, g.filter, g.filterCol)
	if g.sortCol >= 0 && g.sortCol < len(res.Columns) {
		rows = sortRows(rows, g.sortCol, g.sortDesc)
	}
	g.rows = rows
	g.clamp(len(res.Columns))
}

// clamp keeps the selection inside the rows and columns that exist.
func (g *gridModel) clamp(cols int) {
	g.row = min(max(g.row, 0), max(len(g.rows)-1, 0))
	g.col = min(max(g.col, 0), max(cols-1, 0))
	g.colLeft = min(max(g.colLeft, 0), g.col)
}

// filterRows keeps the rows matching filter, case-insensitively, looking at
// every column or at just one. A blank filter keeps everything, and the rows
// themselves are shared rather than copied.
func filterRows(rows [][]string, filter string, col int) [][]string {
	needle := strings.ToLower(strings.TrimSpace(filter))
	if needle == "" {
		return rows
	}
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		if rowMatches(row, needle, col) {
			out = append(out, row)
		}
	}
	return out
}

// rowMatches reports whether a row carries the needle, which is already lower
// case.
func rowMatches(row []string, needle string, col int) bool {
	if col >= 0 {
		return col < len(row) && strings.Contains(strings.ToLower(row[col]), needle)
	}
	for _, cell := range row {
		if strings.Contains(strings.ToLower(cell), needle) {
			return true
		}
	}
	return false
}

// sortRows returns the rows ordered by one column. Columns holding only numbers
// compare numerically, everything else compares case-insensitively, and rows
// with equal keys keep the order they came in.
func sortRows(rows [][]string, col int, desc bool) [][]string {
	out := slices.Clone(rows)
	numeric := numericColumn(out, col)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := cellAt(out[i], col), cellAt(out[j], col)
		if desc {
			a, b = b, a
		}
		return lessCell(a, b, numeric)
	})
	return out
}

// numericColumn reports whether the column holds numbers wherever it holds
// anything, which is what makes 2, 9, 10 sort in that order rather than as
// text. Empty cells and NULLs do not disqualify a column.
func numericColumn(rows [][]string, col int) bool {
	seen := false
	for _, row := range rows {
		cell := cellAt(row, col)
		if cell == "" || cell == nullCell {
			continue
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(cell), 64); err != nil {
			return false
		}
		seen = true
	}
	return seen
}

// lessCell orders two cells. In a numeric column the values compare as numbers
// and anything unparsable — a NULL, an empty cell — sorts after them.
func lessCell(a, b string, numeric bool) bool {
	if numeric {
		af, aok := parseCell(a)
		bf, bok := parseCell(b)
		switch {
		case aok && bok:
			return af < bf
		case aok != bok:
			return aok // a real number comes before a NULL or a blank
		}
	}
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la == lb {
		return false // equal keys keep their order, the sort being stable
	}
	return la < lb
}

func parseCell(s string) (float64, bool) {
	if s == "" || s == nullCell {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

func cellAt(row []string, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	return row[col]
}

// --- key handling -----------------------------------------------------------

// updateGrid handles a key while the results pane is focused and a grid is on
// show. The bool reports whether it consumed the key.
func (m *model) updateGrid(msg tea.KeyMsg) bool {
	q := &m.query
	if q.grid.filtering {
		m.updateGridFilter(msg)
		return true
	}
	if res, _, ok := q.focusedResult(); !ok || len(res.Columns) == 0 {
		// A statement that returned no table has no cells to move between, so
		// the keys keep their old meaning of scrolling the results.
		return false
	}
	switch msg.String() {
	case "j", "down":
		q.moveCell(1, 0)
	case "k", "up":
		q.moveCell(-1, 0)
	case "l", "right":
		q.moveCell(0, 1)
	case "h", "left":
		q.moveCell(0, -1)
	case "g", "home":
		q.grid.row = 0
		q.showCell()
	case "G", "end":
		q.grid.row = max(len(q.grid.rows)-1, 0)
		q.showCell()
	case "o":
		m.sortByColumn()
	case "/":
		q.startGridFilter(filterAllColumns)
	case "f":
		q.startGridFilter(q.grid.col)
	case "enter":
		m.openInspector()
	default:
		return false
	}
	return true
}

// moveCell shifts the selection and scrolls the results so it stays in view.
func (q *queryModel) moveCell(rows, cols int) {
	g := &q.grid
	g.row += rows
	g.col += cols
	g.clamp(q.gridColumns())
	q.showCell()
}

// gridColumns is how many columns the focused result has.
func (q *queryModel) gridColumns() int {
	res, _, ok := q.focusedResult()
	if !ok {
		return 0
	}
	return len(res.Columns)
}

// sortByColumn cycles the selected column through ascending, descending, and
// back to the order the engine returned.
func (m *model) sortByColumn() {
	q := &m.query
	g := &q.grid
	res, _, ok := q.focusedResult()
	if !ok || len(res.Columns) == 0 {
		return
	}
	switch {
	case g.sortCol != g.col:
		g.sortCol, g.sortDesc = g.col, false
	case !g.sortDesc:
		g.sortDesc = true
	default:
		g.sortCol, g.sortDesc = sortNone, false
	}
	q.rebuildGrid()
	q.renderResults()
	q.showCell()
	m.status = stDim.Render(q.sortStatus(res))
}

// sortStatus says in words what the header marks with an arrow.
func (q *queryModel) sortStatus(res db.Result) string {
	g := &q.grid
	if g.sortCol == sortNone {
		return "sorted by nothing – the engine's own order"
	}
	direction := "ascending"
	if g.sortDesc {
		direction = "descending"
	}
	return fmt.Sprintf("sorted by %s, %s", columnName(res, g.sortCol), direction)
}

// startGridFilter opens filter entry over one column, or over all of them when
// col is filterAllColumns.
func (q *queryModel) startGridFilter(col int) {
	if _, _, ok := q.focusedResult(); !ok {
		return
	}
	q.grid.filtering = true
	q.grid.filterCol = col
	q.rebuildGrid()
}

// updateGridFilter handles keys while filter entry is active: runes narrow the
// rows as they are typed, enter keeps the filter, esc drops it.
func (m *model) updateGridFilter(msg tea.KeyMsg) {
	q := &m.query
	g := &q.grid
	switch msg.String() {
	case "esc":
		g.filtering = false
		g.filter = ""
	case "enter":
		g.filtering = false
	case "backspace":
		if r := []rune(g.filter); len(r) > 0 {
			g.filter = string(r[:len(r)-1])
		}
	case "ctrl+u":
		g.filter = ""
	case "down", "ctrl+n":
		g.row++
	case "up", "ctrl+p":
		g.row--
	default:
		switch {
		case msg.Type == tea.KeyRunes && !msg.Alt:
			g.filter += string(msg.Runes)
		case msg.Type == tea.KeySpace:
			g.filter += " "
		}
	}
	q.rebuildGrid()
	q.renderResults()
	q.showCell()
	m.status = stDim.Render(q.filterStatus())
}

// filterStatus reports how much of the result the filter lets through.
func (q *queryModel) filterStatus() string {
	g := &q.grid
	if g.filter == "" {
		return fmt.Sprintf("%d row(s)", g.loaded)
	}
	return fmt.Sprintf("%d of %d row(s) match %q", len(g.rows), g.loaded, g.filter)
}

// gridNote is what the statement's title line says about how its rows are
// being read: the filter and its counts, and the column they are sorted by.
// A result nobody is reading through the grid says nothing.
func (q *queryModel) gridNote(res db.Result, active bool) string {
	if !active || len(res.Columns) == 0 {
		return ""
	}
	g := &q.grid
	var parts []string
	if g.filter != "" || g.filtering {
		parts = append(parts, fmt.Sprintf("filter %q · %d of %d", g.filter, len(g.rows), g.loaded))
	}
	if g.sortCol != sortNone {
		arrow := "▲"
		if g.sortDesc {
			arrow = "▼"
		}
		parts = append(parts, "sorted by "+columnName(res, g.sortCol)+" "+arrow)
	}
	return strings.Join(parts, " · ")
}

// showCell scrolls the results so the selected cell is on screen.
func (q *queryModel) showCell() {
	if q.resSel < 0 || q.resSel >= len(q.resOffsets) {
		return
	}
	line := q.resOffsets[q.resSel] + gridHeaderLines + q.grid.row
	switch {
	case line < q.vp.YOffset:
		q.vp.SetYOffset(line)
	case line >= q.vp.YOffset+q.vp.Height:
		q.vp.SetYOffset(line - q.vp.Height + 1)
	}
}

// --- the cell inspector -----------------------------------------------------

// inspectModel is the full-screen view of one cell. open = false means it is
// closed.
type inspectModel struct {
	open  bool
	title string
	raw   string // the value as the engine returned it, which is what copying yields
	vp    viewport.Model
}

// cellCopiedMsg reports the outcome of copying a cell value.
type cellCopiedMsg struct{ err error }

// openInspector shows the selected cell full-screen.
func (m *model) openInspector() {
	q := &m.query
	res, _, ok := q.focusedResult()
	if !ok || len(q.grid.rows) == 0 {
		m.status = "no cell to inspect"
		return
	}
	raw := cellAt(q.grid.rows[q.grid.row], q.grid.col)
	width, height := m.inspectorSize()
	vp := viewport.New(width, height)
	vp.SetContent(inspectBody(raw, width))
	q.inspect = inspectModel{
		open:  true,
		title: fmt.Sprintf("%s · row %d of %d", columnName(res, q.grid.col), q.grid.row+1, len(q.grid.rows)),
		raw:   raw,
		vp:    vp,
	}
}

// inspectorSize is the room the inspector has inside its frame.
func (m *model) inspectorSize() (width, height int) {
	width = max(m.width-8, 20)
	height = max(m.bodyHeight()-6, 3)
	return width, height
}

// inspectBody is what the inspector shows: JSON indented so it can be read,
// anything else wrapped as it came.
func inspectBody(raw string, width int) string {
	if pretty, ok := prettyJSON(raw); ok {
		return pretty
	}
	return wrapText(raw, width)
}

// prettyJSON indents a JSON document. The bool is false for anything that is
// not JSON, which is left alone rather than mangled.
func prettyJSON(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 2 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(trimmed), "", "  "); err != nil {
		return "", false
	}
	return buf.String(), true
}

// wrapText breaks long lines at the given width so nothing runs off the side.
func wrapText(s string, width int) string {
	if width < 1 {
		return s
	}
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		for runes := []rune(line); ; {
			if len(runes) <= width {
				b.WriteString(string(runes))
				break
			}
			b.WriteString(string(runes[:width]) + "\n")
			runes = runes[width:]
		}
	}
	return b.String()
}

// updateInspect handles keys while the inspector is open: esc closes it, y
// copies the raw value, everything else scrolls.
func (m *model) updateInspect(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	switch msg.String() {
	case "esc", "q", "enter":
		q.inspect = inspectModel{}
		return nil
	case "y":
		raw := q.inspect.raw
		return func() tea.Msg { return cellCopiedMsg{err: export.CopyText(raw)} }
	}
	var cmd tea.Cmd
	q.inspect.vp, cmd = q.inspect.vp.Update(msg)
	return cmd
}

func (m *model) handleCellCopied(msg cellCopiedMsg) {
	if msg.err != nil {
		m.status = stErr.Render(msg.err.Error())
		return
	}
	m.status = stOK.Render("copied the cell value to the clipboard")
}

// inspectView renders the inspector over the whole body area.
func (q *queryModel) inspectView() string {
	var b strings.Builder
	b.WriteString(stSection.Render(clip(q.inspect.title, q.inspect.vp.Width)) + "\n\n")
	b.WriteString(q.inspect.vp.View() + "\n\n")
	b.WriteString(stDim.Render("↑/↓ scroll · y copy the raw value · esc close"))
	return stHelpBox.Render(b.String())
}

// --- rendering --------------------------------------------------------------

// columnName is a column's name, or its position when the driver named none.
func columnName(res db.Result, col int) string {
	if col >= 0 && col < len(res.Columns) {
		return res.Columns[col]
	}
	return fmt.Sprintf("column %d", col+1)
}

// columnType is the engine's type name for a column, empty when the driver
// does not report one.
func columnType(res db.Result, col int) string {
	if col >= 0 && col < len(res.ColumnTypes) {
		return res.ColumnTypes[col]
	}
	return ""
}

// gridHeader is the text of one column's header: its name, the engine's type
// where there is one, and the sort marker when the rows are ordered by it.
func gridHeader(res db.Result, col int, g gridModel, active bool) string {
	head := columnName(res, col)
	if t := columnType(res, col); t != "" {
		head += " " + t
	}
	if active && g.sortCol == col {
		if g.sortDesc {
			return head + " ▼"
		}
		return head + " ▲"
	}
	return head
}

// renderGrid draws one result as a table. The focused result — active — shows
// its rows filtered and sorted with the selected cell highlighted; the others
// render as the engine returned them.
func (q *queryModel) renderGrid(res db.Result, active bool, width int) string {
	g := gridModel{sortCol: sortNone, filterCol: filterAllColumns}
	rows := res.Rows
	if active {
		g = q.grid
		rows = g.rows
	}

	heads := make([]string, len(res.Columns))
	widths := make([]int, len(res.Columns))
	for i := range res.Columns {
		heads[i] = clip(gridHeader(res, i, g, active), maxColWidth)
		widths[i] = len([]rune(heads[i]))
	}
	for _, row := range rows {
		for i := range widths {
			if n := len([]rune(clip(cellAt(row, i), maxColWidth))); n > widths[i] {
				widths[i] = n
			}
		}
	}

	first, last := visibleColumns(widths, g.colLeft, g.col, width)
	if active {
		q.grid.colLeft = first
	}

	var b strings.Builder
	b.WriteString(gridLine(heads, widths, first, last, -1, true) + "\n")
	rules := make([]string, len(widths))
	for i, w := range widths {
		rules[i] = strings.Repeat("─", w)
	}
	b.WriteString(stDim.Render(strings.Join(rules[first:last], "─┼─")) + "\n")
	for i, row := range rows {
		cells := make([]string, len(widths))
		for c := range widths {
			cells[c] = clip(cellAt(row, c), maxColWidth)
		}
		selected := -1
		if active && i == g.row {
			selected = g.col
		}
		b.WriteString(gridLine(cells, widths, first, last, selected, false) + "\n")
	}
	return b.String()
}

// gridLine renders one row of the table between the first and last visible
// columns, marking the selected cell and flagging columns left off either side.
func gridLine(cells []string, widths []int, first, last, selected int, head bool) string {
	out := make([]string, 0, last-first)
	for i := first; i < last; i++ {
		cell := pad(cells[i], widths[i])
		switch {
		case i == selected:
			cell = stSelected.Render(cell)
		case head:
			cell = stTableHead.Render(cell)
		}
		out = append(out, cell)
	}
	line := strings.Join(out, " │ ")
	if first > 0 {
		line = stDim.Render("‹ ") + line
	}
	if last < len(widths) {
		line += stDim.Render(" ›")
	}
	return line
}

// visibleColumns picks the run of columns to draw: as many as fit in width,
// starting at left and shifted only as far as it takes to show the selected
// column.
func visibleColumns(widths []int, left, selected, width int) (first, last int) {
	if len(widths) == 0 {
		return 0, 0
	}
	first = min(max(left, 0), len(widths)-1)
	selected = min(max(selected, 0), len(widths)-1)
	if selected < first {
		first = selected
	}
	for {
		last = first
		for used := 0; last < len(widths); last++ {
			used += widths[last] + gridColGap
			if last > first && used > width {
				break
			}
		}
		if selected < last || first >= selected {
			return first, last
		}
		first++
	}
}
