package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/history"
)

// histMaxRows is the most history entries listed at once; the list scrolls to
// keep the selection visible.
const histMaxRows = 10

// historyLoadedMsg carries the history file read off the UI goroutine.
type historyLoadedMsg struct {
	entries []history.Entry
	err     error
}

// openHistory shows the history overlay and starts loading the file.
func (m *model) openHistory() tea.Cmd {
	q := &m.query
	if m.hist == nil {
		m.status = stErr.Render("history unavailable: " + m.histErr)
		return nil
	}
	q.histOpen = true
	q.histLoading = true
	q.histErr = ""
	q.histFilter = ""
	q.histSel = 0
	q.histAll, q.histShown = nil, nil
	return loadHistoryCmd(m.hist)
}

func loadHistoryCmd(s *history.Store) tea.Cmd {
	return func() tea.Msg {
		entries, err := s.Load()
		return historyLoadedMsg{entries: entries, err: err}
	}
}

func (m *model) handleHistoryLoaded(msg historyLoadedMsg) {
	q := &m.query
	if !q.histOpen {
		// The user closed the overlay while the file was being read.
		return
	}
	q.histLoading = false
	if msg.err != nil {
		q.histErr = msg.err.Error()
		return
	}
	q.histAll = msg.entries
	q.applyHistFilter()
}

// recordHistory appends one executed statement to the store. It runs on the
// run goroutine, so it takes the store instead of reaching into the model.
func recordHistory(s *history.Store, connName, dbName, statement string, ok bool, dur time.Duration) {
	if s == nil {
		return
	}
	// History is a convenience: a store that cannot be written must not
	// disturb the run in progress.
	_ = s.Append(history.Entry{
		Timestamp:  time.Now(),
		Connection: connName,
		Database:   dbName,
		Statement:  statement,
		OK:         ok,
		Duration:   dur,
	})
}

// --- key handling ---------------------------------------------------------

// updateHistory handles keys while the history overlay is open: printable
// runes filter, j/k (with an empty filter) and the arrows move the selection,
// enter inserts the statement into the editor, esc closes.
func (m *model) updateHistory(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	switch msg.String() {
	case "esc", "ctrl+h":
		q.closeHistory()
		return nil
	case "enter":
		return m.insertFromHistory()
	case "down", "ctrl+n":
		q.histMove(1)
		return nil
	case "up", "ctrl+p":
		q.histMove(-1)
		return nil
	case "j":
		if q.histFilter == "" {
			q.histMove(1)
			return nil
		}
	case "k":
		if q.histFilter == "" {
			q.histMove(-1)
			return nil
		}
	case "backspace":
		if r := []rune(q.histFilter); len(r) > 0 {
			q.setHistFilter(string(r[:len(r)-1]))
		}
		return nil
	case "ctrl+u":
		q.setHistFilter("")
		return nil
	}

	switch {
	case msg.Type == tea.KeyRunes && !msg.Alt:
		q.setHistFilter(q.histFilter + string(msg.Runes))
	case msg.Type == tea.KeySpace:
		q.setHistFilter(q.histFilter + " ")
	}
	return nil
}

// insertFromHistory closes the overlay and puts the selected statement in the
// editor. The statement is never executed by this path.
func (m *model) insertFromHistory() tea.Cmd {
	q := &m.query
	if q.histSel < 0 || q.histSel >= len(q.histShown) {
		q.closeHistory()
		return nil
	}
	stmt := q.histShown[q.histSel].Statement
	q.closeHistory()
	q.ta.SetValue(stmt)
	q.focusResults = false
	m.status = "inserted from history · ctrl+r to run"
	return q.ta.Focus()
}

func (q *queryModel) closeHistory() {
	q.histOpen = false
	q.histLoading = false
	q.histAll, q.histShown = nil, nil
	q.histFilter = ""
	q.histErr = ""
	q.histSel = 0
}

func (q *queryModel) setHistFilter(s string) {
	q.histFilter = s
	q.histSel = 0
	q.applyHistFilter()
}

func (q *queryModel) applyHistFilter() {
	q.histShown = history.Filter(q.histAll, q.histFilter)
	if q.histSel >= len(q.histShown) {
		q.histSel = len(q.histShown) - 1
	}
	if q.histSel < 0 {
		q.histSel = 0
	}
}

// histMove moves the selection by delta, clamped to the visible entries.
func (q *queryModel) histMove(delta int) {
	sel := q.histSel + delta
	if last := len(q.histShown) - 1; sel > last {
		sel = last
	}
	if sel < 0 {
		sel = 0
	}
	q.histSel = sel
}

// --- rendering ------------------------------------------------------------

// historyView renders the overlay box for the given body area.
func (q *queryModel) historyView(width, height int) string {
	inner := min(width-10, maxStmtWidth)
	if inner < 24 {
		inner = 24
	}
	rows := min(histMaxRows, height-8)
	if rows < 1 {
		rows = 1
	}

	var b strings.Builder
	b.WriteString(stSection.Render("Query history") + "\n")
	filter := q.histFilter
	if filter == "" {
		filter = stDim.Render("type to filter")
	}
	b.WriteString(stDim.Render("filter: ") + filter + "\n\n")
	b.WriteString(q.historyList(inner, rows))
	b.WriteString("\n" + stDim.Render("j/k or ↑/↓ select · enter insert (does not run) · esc close"))
	return stHelpBox.Render(b.String())
}

func (q *queryModel) historyList(width, rows int) string {
	switch {
	case q.histErr != "":
		return stErr.Render(clip(q.histErr, width)) + "\n"
	case q.histLoading:
		return stDim.Render("loading...") + "\n"
	case len(q.histAll) == 0:
		return stDim.Render("no history yet – statements are recorded as you run them") + "\n"
	case len(q.histShown) == 0:
		return stDim.Render(clip(fmt.Sprintf("no statement matches %q", q.histFilter), width)) + "\n"
	}

	start := 0
	if q.histSel >= rows {
		start = q.histSel - rows + 1
	}
	end := min(start+rows, len(q.histShown))

	metaW := width / 3
	stmtW := width - metaW - 4 // cursor, outcome mark and two spaces

	var b strings.Builder
	for i := start; i < end; i++ {
		e := q.histShown[i]
		mark := stOK.Render("✓")
		if !e.OK {
			mark = stErr.Render("✗")
		}
		stmt := pad(clip(e.Statement, stmtW), stmtW)
		cursor := "  "
		if i == q.histSel {
			cursor = stSelected.Render("> ")
			stmt = stSelected.Render(stmt)
		}
		meta := clip(fmt.Sprintf("%s · %s", location(e), age(e.Timestamp)), metaW)
		b.WriteString(cursor + mark + " " + stmt + " " + stDim.Render(meta) + "\n")
	}
	if n := len(q.histShown) - end + start; n > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  %d more (%d total)", n, len(q.histShown))) + "\n")
	}
	return b.String()
}

// location is the connection/database an entry was run against.
func location(e history.Entry) string {
	if e.Database == "" {
		return e.Connection
	}
	return e.Connection + "/" + e.Database
}

// age renders how long ago t was, compactly.
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
