package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/history"
	"github.com/lergor11/d9s/internal/schema"
)

const (
	maxColWidth  = 32
	maxStmtWidth = 100
)

type execResultMsg struct{ res db.Result }
type execDoneMsg struct{}

type queryModel struct {
	ta textarea.Model
	vp viewport.Model

	driver   db.Driver
	engine   config.EngineType
	connName string
	dbName   string

	focusResults bool
	running      bool
	cancelled    bool
	cancel       context.CancelFunc
	ch           chan db.Result
	results      []db.Result
	confirm      []string // flagged statements awaiting confirmation; nil = no modal
	pending      []string // full statement list held while confirming

	resSel     int   // highlighted statement section in the results area
	resOffsets []int // first rendered line of each statement section

	// Export prompt state; exportPrompt = false means no prompt is showing.
	exportPrompt bool
	exportPath   string
	exportIdx    int       // 1-based index of the result being exported
	exportRes    db.Result // captured when the prompt opens

	// History overlay state; histOpen = false means the overlay is closed.
	histOpen    bool
	histLoading bool
	histErr     string
	histAll     []history.Entry // newest first, de-duplicated
	histShown   []history.Entry // histAll narrowed by histFilter
	histFilter  string
	histSel     int

	// Schema panel state; schema.open = false means the panel is closed.
	schema schemaModel

	// How the focused result is being read, and the full-screen view of one of
	// its cells; inspect.open = false means no cell is open.
	grid    gridModel
	inspect inspectModel

	// Saved-query browser; saved.open = false means it is closed.
	saved savedModel

	// Tab completion: the popup state and the session's name cache, which is
	// filled in the background on the first completion that needs it.
	comp  completionModel
	cache *schema.Cache

	// First row and column of the editor's visible window. The editor draws
	// its own highlighted view, so it also does its own scrolling.
	edTop, edLeft int

	width, height int
}

func (q *queryModel) open(driver db.Driver, engine config.EngineType, connName, dbName string) {
	q.close()
	ta := textarea.New()
	ta.Placeholder = placeholderFor(engine)
	// The editor renders its own gutter, line numbers and highlighted text; the
	// widget keeps the buffer and the cursor. Lines never wrap, so what the
	// widget's cursor movement assumes and what the screen shows stay the same.
	ta.ShowLineNumbers = false
	ta.MaxWidth = 0
	ta.SetWidth(noWrapWidth)
	q.ta = ta
	q.edTop, q.edLeft = 0, 0
	q.vp = viewport.New(0, 0)
	q.driver = driver
	q.engine = engine
	q.connName = connName
	q.dbName = dbName
	q.focusResults = false
	q.results = nil
	q.resSel = 0
	q.resOffsets = nil
	q.confirm = nil
	q.pending = nil
	q.closeExportPrompt()
	q.running = false
	q.cancelled = false
	q.closeHistory()
	q.closeSchema()
	q.closeSaved()
	q.comp.close()
	q.grid.reset()
	q.inspect = inspectModel{}
	q.cache = schema.New(driver)
}

func placeholderFor(engine config.EngineType) string {
	if engine == config.Redis {
		return "one command per line, e.g. GET mykey"
	}
	return "SELECT ... ;  (ctrl+r to run)"
}

// close cancels any in-flight run and releases the session driver.
func (q *queryModel) close() {
	if q.cancel != nil {
		q.cancel()
		q.cancel = nil
	}
	if q.driver != nil {
		drv := q.driver
		go func() { _ = drv.Close() }()
		q.driver = nil
	}
	q.comp.close()
	q.cache = nil
	q.running = false
}

func (q *queryModel) editorFocused() bool {
	return !q.focusResults && q.confirm == nil && !q.histOpen && !q.schema.open &&
		!q.exportPrompt && !q.saved.open && !q.inspect.open
}

// capturesKeys reports whether the query view consumes every key press, which
// leaves no global single-key shortcut (such as '?') active.
func (q *queryModel) capturesKeys() bool {
	return q.editorFocused() || q.confirm != nil || q.histOpen || q.schema.open ||
		q.exportPrompt || q.saved.open || q.inspect.open || q.grid.filtering
}

func (q *queryModel) layout(width, bodyHeight int) {
	if q.driver == nil || width == 0 {
		return
	}
	q.width, q.height = width, bodyHeight
	edH := bodyHeight / 3
	if edH < 3 {
		edH = 3
	}
	if edH > 10 {
		edH = 10
	}
	vpH := bodyHeight - edH - 1 // one divider line
	if vpH < 1 {
		vpH = 1
	}
	q.ta.SetHeight(edH)
	q.vp.Width = width
	q.vp.Height = vpH
}

func (m *model) updateQuery(msg tea.KeyMsg) tea.Cmd {
	q := &m.query

	// Destructive-statement confirmation modal.
	if q.confirm != nil {
		switch msg.String() {
		case "y", "Y":
			stmts := q.pending
			q.confirm, q.pending = nil, nil
			return m.startRun(stmts)
		case "n", "N", "esc":
			q.confirm, q.pending = nil, nil
			m.status = "run cancelled, nothing executed"
		}
		return nil
	}

	// The prompts, overlays and panels each take every key while they show.
	if q.exportPrompt {
		return m.updateExportPrompt(msg)
	}
	if q.histOpen {
		return m.updateHistory(msg)
	}
	if q.schema.open {
		return m.updateSchema(msg)
	}
	if q.saved.open {
		return m.updateSaved(msg)
	}
	if q.inspect.open {
		return m.updateInspect(msg)
	}

	// The completion popup takes the keys that drive it; the rest fall through
	// to the editor, which narrows the list as the name grows.
	if q.comp.open && m.updateCompletion(msg) {
		return nil
	}

	// With the results focused the grid takes the movement keys, so the cell
	// cursor answers to j/k/h/l before anything else looks at them.
	if q.focusResults && m.updateGrid(msg) {
		return nil
	}

	switch msg.String() {
	case "ctrl+r", "f5":
		return m.startRunOrConfirm()
	case "ctrl+h":
		return m.openHistory()
	case "ctrl+s":
		return m.openSchemaPanel()
	case "ctrl+o":
		return m.openSaved()
	case "s":
		// Bare letters are editor input; they act as commands only when the
		// results pane has focus.
		if q.focusResults {
			return m.openSchemaPanel()
		}
	case "e":
		if q.focusResults {
			return m.openExportPrompt()
		}
	case "y":
		if q.focusResults {
			return m.copyFocusedResult()
		}
	case "n":
		if q.focusResults {
			q.focusResult(1)
			return nil
		}
	case "N":
		if q.focusResults {
			q.focusResult(-1)
			return nil
		}
	case "ctrl+x":
		if q.running && q.cancel != nil {
			q.cancelled = true
			q.cancel()
			m.status = "cancelling..."
		}
		return nil
	case "tab":
		// Tab completes in the editor, so only the results pane still toggles
		// focus with it; ctrl+j toggles from either side.
		if !q.focusResults {
			return m.startCompletion()
		}
		return m.toggleQueryFocus()
	case "ctrl+j":
		return m.toggleQueryFocus()
	case "ctrl+g":
		if !q.focusResults {
			m.refreshCompletionSchema()
			return nil
		}
	case "esc":
		if q.running {
			m.status = "query running – ctrl+x to cancel first"
			return nil
		}
		q.close()
		m.view = viewDatabases
		return nil
	}

	var cmd tea.Cmd
	if q.focusResults {
		q.vp, cmd = q.vp.Update(msg)
	} else {
		q.ta, cmd = q.ta.Update(msg)
		q.narrowCompletion()
	}
	return cmd
}

// openSchemaPanel opens the schema panel unless a completion is already using
// the session driver, which engines such as postgres cannot share.
func (m *model) openSchemaPanel() tea.Cmd {
	if q := &m.query; q.cache != nil && q.cache.Loading() {
		m.status = "schema is loading – try again in a moment"
		return nil
	}
	return m.openSchema()
}

// toggleQueryFocus moves the focus between the editor and the results pane.
func (m *model) toggleQueryFocus() tea.Cmd {
	q := &m.query
	q.comp.close()
	q.focusResults = !q.focusResults
	q.renderResults() // the statement cursor only shows with results focused
	if q.focusResults {
		q.ta.Blur()
		return nil
	}
	return q.ta.Focus()
}

func (m *model) startRunOrConfirm() tea.Cmd {
	q := &m.query
	if q.running {
		m.status = "a run is already in progress"
		return nil
	}
	if q.schema.inflight || (q.cache != nil && q.cache.Loading()) {
		// The schema panel or a completion is using the same session; engines
		// such as postgres hold a single connection that is not safe to share.
		m.status = "schema is loading – try again in a moment"
		return nil
	}
	stmts := db.Split(q.engine, q.ta.Value())
	if len(stmts) == 0 {
		m.status = "nothing to run"
		return nil
	}
	if flagged := db.Destructive(q.engine, stmts); len(flagged) > 0 {
		q.pending = stmts
		q.confirm = flagged
		return nil
	}
	return m.startRun(stmts)
}

// startRun executes the statements sequentially in a background goroutine,
// streaming one Result per statement back into the update loop.
func (m *model) startRun(stmts []string) tea.Cmd {
	q := &m.query
	ctx, cancel := context.WithCancel(context.Background())
	q.cancel = cancel
	q.running = true
	q.cancelled = false
	q.results = nil
	q.resSel = 0
	q.grid.reset()
	q.renderResults()
	ch := make(chan db.Result, len(stmts))
	q.ch = ch
	driver := q.driver
	connName, dbName, hist := q.connName, q.dbName, m.hist
	go func() {
		defer close(ch)
		stopped := false
		for _, s := range stmts {
			if stopped || ctx.Err() != nil {
				// Skipped statements never reached the engine, so they are
				// not recorded in the history.
				ch <- db.Result{Statement: s, Skipped: true, Affected: -1}
				continue
			}
			res := driver.Execute(ctx, s)
			recordHistory(hist, connName, dbName, s, res.Err == nil, res.Duration)
			ch <- res
			if res.Err != nil || ctx.Err() != nil {
				stopped = true
			}
		}
	}()
	return tea.Batch(m.spinner.Tick, waitExec(ch))
}

func waitExec(ch chan db.Result) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return execDoneMsg{}
		}
		return execResultMsg{res: res}
	}
}

func (m *model) handleExecMsg(msg tea.Msg) tea.Cmd {
	q := &m.query
	switch msg := msg.(type) {
	case execResultMsg:
		q.results = append(q.results, msg.res)
		if q.resSel >= len(q.results) {
			q.resSel = len(q.results) - 1
		}
		q.rebuildGrid()
		q.renderResults()
		return waitExec(q.ch)
	case execDoneMsg:
		q.running = false
		if q.cancel != nil {
			q.cancel()
			q.cancel = nil
		}
		var rows int64
		var dur time.Duration
		failed := 0
		for _, r := range q.results {
			rows += int64(len(r.Rows))
			dur += r.Duration
			if r.Err != nil {
				failed++
			}
		}
		summary := fmt.Sprintf("%d statement(s), %d row(s) in %s", len(q.results), rows, dur.Round(time.Millisecond))
		switch {
		case q.cancelled:
			m.status = stWarn.Render("cancelled · " + summary)
		case failed > 0:
			m.status = stErr.Render("failed · " + summary)
		default:
			m.status = stOK.Render(summary)
		}
		q.renderResults()
	}
	return nil
}

// --- rendering ------------------------------------------------------------

func (q *queryModel) renderResults() {
	atBottom := q.vp.AtBottom()
	q.vp.SetContent(q.resultsContent())
	if q.running && atBottom {
		q.vp.GotoBottom()
	}
}

func (q *queryModel) resultsContent() string {
	q.resOffsets = nil
	if len(q.results) == 0 {
		if q.running {
			return stDim.Render("running...")
		}
		return stDim.Render("no results yet – ctrl+r runs the editor buffer")
	}
	w := q.vp.Width
	if w <= 0 {
		w = 80
	}
	var b strings.Builder
	line := 0
	for i, r := range q.results {
		if i > 0 {
			b.WriteString("\n")
			line++
		}
		q.resOffsets = append(q.resOffsets, line)
		cursor := "  "
		if q.focusResults && i == q.resSel {
			cursor = stSelected.Render("> ")
		}
		active := q.focusResults && i == q.resSel
		title := fmt.Sprintf("[%d] %s", i+1, clip(r.Statement, min(maxStmtWidth, w-8)))
		if note := q.gridNote(r, active); note != "" {
			title += " · " + note
		}
		section := cursor + stSection.Render(truncate(title, w-2)) + "\n" + q.resultBody(r, active, w)
		b.WriteString(section)
		line += strings.Count(section, "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// resultBody renders everything below a statement's title line. The focused
// result — active — is the one the grid's sort, filter and cell cursor apply to.
func (q *queryModel) resultBody(r db.Result, active bool, width int) string {
	switch {
	case r.Skipped:
		return stDim.Render("skipped") + "\n"
	case r.Err != nil:
		return stErr.Render(r.Err.Error()) + "\n" +
			stDim.Render(r.Duration.Round(time.Millisecond).String()) + "\n"
	case len(r.Columns) > 0:
		return q.renderGrid(r, active, width) +
			stDim.Render(fmt.Sprintf("%d row(s) in %s", len(r.Rows), r.Duration.Round(time.Millisecond))) + "\n"
	default:
		affected := "OK"
		if r.Affected >= 0 {
			affected = fmt.Sprintf("%d row(s) affected", r.Affected)
		}
		return affected + " " + stDim.Render("in "+r.Duration.Round(time.Millisecond).String()) + "\n"
	}
}

// focusResult moves the highlighted statement section by delta and scrolls it
// to the top of the results viewport.
func (q *queryModel) focusResult(delta int) {
	if len(q.results) == 0 {
		return
	}
	sel := q.resSel + delta
	if sel > len(q.results)-1 {
		sel = len(q.results) - 1
	}
	if sel < 0 {
		sel = 0
	}
	q.resSel = sel
	q.grid.reset()
	q.rebuildGrid()
	q.renderResults()
	if sel < len(q.resOffsets) {
		q.vp.SetYOffset(q.resOffsets[sel])
	}
}

func (q *queryModel) view(spin string) string {
	divider := " editor "
	if q.focusResults {
		divider = " results "
	}
	line := stDim.Render(strings.Repeat("─", max(0, 3))) + stSection.Render(divider)
	if q.width > lipgloss.Width(line) {
		line += stDim.Render(strings.Repeat("─", q.width-lipgloss.Width(line)))
	}
	return q.editorView() + "\n" + line + "\n" + q.resultsArea(spin)
}

// resultsArea is the results viewport with the completion popup laid over its
// first lines, keeping the height the viewport already claimed.
func (q *queryModel) resultsArea(spin string) string {
	rows := strings.Split(q.vp.View(), "\n")
	popup := q.completionView(q.width, spin)
	if popup == "" {
		return strings.Join(rows, "\n")
	}
	over := strings.Split(popup, "\n")
	if len(over) >= len(rows) {
		return strings.Join(over[:len(rows)], "\n")
	}
	return strings.Join(append(over, rows[len(over):]...), "\n")
}

func (q *queryModel) confirmView(width int) string {
	var b strings.Builder
	b.WriteString(stWarn.Render("Destructive statements detected") + "\n\n")
	limit := min(maxStmtWidth, width-10)
	for _, s := range q.confirm {
		b.WriteString("  " + stErr.Render(clip(s, limit)) + "\n")
	}
	b.WriteString("\nRun the whole buffer anyway?\n")
	b.WriteString(stOK.Render("y") + " run all   " + stErr.Render("n/esc") + " cancel (nothing runs)")
	return stModal.Render(b.String())
}
