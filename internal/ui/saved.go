package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/snippets"
)

// savedMaxRows is the most saved queries listed at once; the list scrolls to
// keep the selection visible.
const savedMaxRows = 10

// savedConfirm is a yes/no question the browser is waiting on.
type savedConfirm int

const (
	confirmNone savedConfirm = iota
	// confirmOverwrite asks before replacing a saved query of the same name.
	confirmOverwrite
	// confirmDelete asks before removing the selected saved query.
	confirmDelete
)

// savedModel is the saved-query browser: what the file holds for this
// connection, the filter over it, and the prompt or question currently in
// front of the list. open = false means the browser is closed.
type savedModel struct {
	open    bool
	loading bool
	errMsg  string

	all       []snippets.Scoped // global entries plus this connection's
	shown     []snippets.Scoped // all, narrowed by filter
	filter    string
	filtering bool // '/' filter entry is active
	sel       int

	// Save prompt state; saving = false means no prompt is showing.
	saving bool
	name   string
	scope  snippets.Scope

	confirm savedConfirm
}

// savedLoadedMsg carries the queries file read off the UI goroutine.
type savedLoadedMsg struct {
	entries []snippets.Entry
	err     error
}

// savedWroteMsg reports a finished write to the queries file.
type savedWroteMsg struct {
	name    string
	deleted bool
	err     error
}

// --- opening and closing ----------------------------------------------------

// openSaved shows the browser and reads the queries file. The file is read on
// every open, so a hand edit outside d9s shows up.
func (m *model) openSaved() tea.Cmd {
	q := &m.query
	if m.snips == nil {
		m.status = stErr.Render("saved queries unavailable: " + m.snipsErr)
		return nil
	}
	q.saved = savedModel{open: true, loading: true, scope: q.savedScope()}
	return loadSavedCmd(m.snips)
}

// savedScope is the scope a new entry defaults to: this connection when there
// is one, so a query about one database does not clutter every other.
func (q *queryModel) savedScope() snippets.Scope {
	if q.connName != "" {
		return snippets.ScopeConnection
	}
	return snippets.ScopeGlobal
}

func (q *queryModel) closeSaved() { q.saved = savedModel{} }

func loadSavedCmd(store *snippets.Store) tea.Cmd {
	return func() tea.Msg {
		entries, err := store.Load()
		return savedLoadedMsg{entries: entries, err: err}
	}
}

func saveSnippetCmd(store *snippets.Store, e snippets.Entry, overwrite bool) tea.Cmd {
	return func() tea.Msg {
		return savedWroteMsg{name: e.Name, err: store.Save(e, overwrite)}
	}
}

func deleteSnippetCmd(store *snippets.Store, name, connection string) tea.Cmd {
	return func() tea.Msg {
		return savedWroteMsg{name: name, deleted: true, err: store.Delete(name, connection)}
	}
}

func (m *model) handleSavedLoaded(msg savedLoadedMsg) {
	q := &m.query
	if !q.saved.open {
		return // the user closed the browser while the file was being read
	}
	q.saved.loading = false
	if msg.err != nil {
		q.saved.errMsg = msg.err.Error()
		return
	}
	q.saved.errMsg = ""
	q.saved.all = snippets.ForConnection(msg.entries, q.connName)
	q.applySavedFilter()
}

// handleSavedWrote finishes a save or a delete: a name that already exists
// turns into the overwrite question rather than an error.
func (m *model) handleSavedWrote(msg savedWroteMsg) tea.Cmd {
	q := &m.query
	if !q.saved.open {
		return nil
	}
	switch {
	case errors.Is(msg.err, snippets.ErrExists):
		q.saved.confirm = confirmOverwrite
		return nil
	case msg.err != nil:
		q.saved.saving, q.saved.confirm = false, confirmNone
		q.saved.errMsg = msg.err.Error()
		return nil
	}
	q.saved.saving, q.saved.confirm = false, confirmNone
	q.saved.name = ""
	verb := "saved"
	if msg.deleted {
		verb = "deleted"
	}
	m.status = stOK.Render(fmt.Sprintf("%s %q", verb, msg.name))
	q.saved.loading = true
	return loadSavedCmd(m.snips)
}

// --- key handling -----------------------------------------------------------

// updateSaved handles keys while the browser is open. The prompts in front of
// the list take every key while they are up.
func (m *model) updateSaved(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	s := &q.saved
	switch {
	case s.confirm != confirmNone:
		return m.updateSavedConfirm(msg)
	case s.saving:
		return m.updateSavePrompt(msg)
	case s.filtering:
		m.updateSavedFilter(msg)
		return nil
	}

	switch msg.String() {
	case "esc", "ctrl+o":
		q.closeSaved()
	case "enter":
		return m.loadSelectedSnippet()
	case "s":
		m.startSavePrompt()
	case "d":
		if len(s.shown) > 0 {
			s.confirm = confirmDelete
		}
	case "/":
		s.filtering = true
	case "j", "down", "ctrl+n":
		q.savedMove(1)
	case "k", "up", "ctrl+p":
		q.savedMove(-1)
	}
	return nil
}

// updateSavedFilter narrows the list as the user types; enter keeps the
// filter, esc drops it.
func (m *model) updateSavedFilter(msg tea.KeyMsg) {
	q := &m.query
	s := &q.saved
	switch msg.String() {
	case "esc":
		s.filtering = false
		s.filter = ""
	case "enter":
		s.filtering = false
	case "backspace":
		if r := []rune(s.filter); len(r) > 0 {
			s.filter = string(r[:len(r)-1])
		}
	case "ctrl+u":
		s.filter = ""
	case "down", "ctrl+n":
		q.savedMove(1)
		return
	case "up", "ctrl+p":
		q.savedMove(-1)
		return
	default:
		switch {
		case msg.Type == tea.KeyRunes && !msg.Alt:
			s.filter += string(msg.Runes)
		case msg.Type == tea.KeySpace:
			s.filter += " "
		default:
			return
		}
	}
	s.sel = 0
	q.applySavedFilter()
}

// startSavePrompt asks for the name to save the editor buffer under.
func (m *model) startSavePrompt() {
	q := &m.query
	if strings.TrimSpace(q.ta.Value()) == "" {
		q.saved.errMsg = "the editor is empty – nothing to save"
		return
	}
	q.saved.saving = true
	q.saved.errMsg = ""
	q.saved.scope = q.savedScope()
	if e, ok := q.selectedSnippet(); ok {
		q.saved.name = e.Name // offer to replace what is highlighted
		q.saved.scope = e.Scope
	}
}

// updateSavePrompt edits the name: tab switches scope, enter saves, esc goes
// back to the list.
func (m *model) updateSavePrompt(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	s := &q.saved
	switch msg.String() {
	case "esc":
		s.saving = false
		s.name = ""
		return nil
	case "tab":
		if s.scope == snippets.ScopeGlobal && q.connName != "" {
			s.scope = snippets.ScopeConnection
		} else {
			s.scope = snippets.ScopeGlobal
		}
		return nil
	case "enter":
		return m.saveBuffer(false)
	case "backspace":
		if r := []rune(s.name); len(r) > 0 {
			s.name = string(r[:len(r)-1])
		}
		return nil
	case "ctrl+u":
		s.name = ""
		return nil
	}
	switch {
	case msg.Type == tea.KeyRunes && !msg.Alt:
		s.name += string(msg.Runes)
	case msg.Type == tea.KeySpace:
		s.name += " "
	}
	return nil
}

// updateSavedConfirm answers the overwrite or delete question.
func (m *model) updateSavedConfirm(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	s := &q.saved
	confirm := s.confirm
	switch msg.String() {
	case "y", "Y":
		s.confirm = confirmNone
		if confirm == confirmDelete {
			return m.deleteSelectedSnippet()
		}
		return m.saveBuffer(true)
	case "n", "N", "esc":
		s.confirm = confirmNone
		if confirm == confirmOverwrite {
			m.status = "not overwritten"
		}
	}
	return nil
}

// saveBuffer writes the editor buffer under the typed name. Without overwrite
// the store refuses an existing name, which turns into the overwrite question.
func (m *model) saveBuffer(overwrite bool) tea.Cmd {
	q := &m.query
	s := &q.saved
	name := strings.TrimSpace(s.name)
	if name == "" {
		s.errMsg = "a saved query needs a name"
		return nil
	}
	entry := snippets.Entry{Name: name, Query: q.ta.Value()}
	if s.scope == snippets.ScopeConnection {
		entry.Connection = q.connName
	}
	s.errMsg = ""
	return saveSnippetCmd(m.snips, entry, overwrite)
}

// deleteSelectedSnippet removes the highlighted entry from the file.
func (m *model) deleteSelectedSnippet() tea.Cmd {
	q := &m.query
	e, ok := q.selectedSnippet()
	if !ok {
		return nil
	}
	return deleteSnippetCmd(m.snips, e.Name, e.Connection)
}

// loadSelectedSnippet closes the browser and puts the highlighted query in the
// editor. It never executes the query.
func (m *model) loadSelectedSnippet() tea.Cmd {
	q := &m.query
	e, ok := q.selectedSnippet()
	if !ok {
		q.closeSaved()
		return nil
	}
	q.closeSaved()
	q.ta.SetValue(e.Query)
	q.focusResults = false
	m.status = fmt.Sprintf("loaded %q · ctrl+r to run", e.Name)
	return q.ta.Focus()
}

func (q *queryModel) selectedSnippet() (snippets.Scoped, bool) {
	s := &q.saved
	if s.sel < 0 || s.sel >= len(s.shown) {
		return snippets.Scoped{}, false
	}
	return s.shown[s.sel], true
}

// savedMove shifts the selection by delta, clamped to the visible entries.
func (q *queryModel) savedMove(delta int) {
	s := &q.saved
	sel := s.sel + delta
	if last := len(s.shown) - 1; sel > last {
		sel = last
	}
	if sel < 0 {
		sel = 0
	}
	s.sel = sel
}

func (q *queryModel) applySavedFilter() {
	s := &q.saved
	s.shown = snippets.FilterByName(s.all, s.filter)
	s.sel = min(max(s.sel, 0), max(len(s.shown)-1, 0))
}

// --- rendering --------------------------------------------------------------

// savedView renders the browser, or the prompt standing in front of it.
func (q *queryModel) savedView(width, height int) string {
	s := &q.saved
	inner := max(min(width-10, maxStmtWidth), 24)
	if s.saving {
		return q.savePromptView(inner)
	}
	if s.confirm == confirmDelete {
		return q.savedConfirmView(inner)
	}

	rows := max(min(savedMaxRows, height-8), 1)
	var b strings.Builder
	b.WriteString(stSection.Render("Saved queries") + "\n")
	if s.filtering || s.filter != "" {
		filter := s.filter
		if filter == "" {
			filter = stDim.Render("type to filter")
		}
		b.WriteString(stDim.Render("/") + filter + "\n")
	}
	b.WriteString("\n" + q.savedList(inner, rows))
	b.WriteString("\n" + stDim.Render(savedHints(s.filtering)))
	return stHelpBox.Render(b.String())
}

// savedHints is the key legend of the browser, shown in it and in the footer.
func savedHints(filtering bool) string {
	if filtering {
		return "type to filter · enter keep · esc drop · ↑/↓ select"
	}
	return "j/k select · enter load (does not run) · s save buffer · d delete · / filter · esc close"
}

func (q *queryModel) savedList(width, rows int) string {
	s := &q.saved
	switch {
	case s.errMsg != "":
		return stErr.Render(clip(s.errMsg, width)) + "\n"
	case s.loading:
		return stDim.Render("loading...") + "\n"
	case len(s.all) == 0:
		return stDim.Render("nothing saved yet – press s to save the editor buffer") + "\n"
	case len(s.shown) == 0:
		return stDim.Render(clip(fmt.Sprintf("no saved query matches %q", s.filter), width)) + "\n"
	}

	start := 0
	if s.sel >= rows {
		start = s.sel - rows + 1
	}
	end := min(start+rows, len(s.shown))

	nameW := 0
	for _, e := range s.shown[start:end] {
		if n := len([]rune(clip(e.Name, width/2))); n > nameW {
			nameW = n
		}
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		e := s.shown[i]
		name := pad(clip(e.Name, width/2), nameW)
		cursor := "  "
		if i == s.sel {
			cursor = stSelected.Render("> ")
			name = stSelected.Render(name)
		}
		scope := stBadge.Render(savedScopeLabel(e))
		b.WriteString(cursor + name + " " + scope + " " +
			stDim.Render(clip(firstLine(e.Query), max(width-nameW-14, 8))) + "\n")
	}
	if n := len(s.shown) - end + start; n > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  %d more (%d total)", n, len(s.shown))) + "\n")
	}
	return b.String()
}

// savedScopeLabel marks which connection an entry belongs to.
func savedScopeLabel(e snippets.Scoped) string {
	if e.Scope == snippets.ScopeConnection {
		return "[" + e.Connection + "]"
	}
	return "[global]"
}

// firstLine is the query's opening line, for the one-line preview in the list.
func firstLine(query string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(query), "\n")
	return line
}

// savePromptView asks for the name and scope to save the buffer under.
func (q *queryModel) savePromptView(width int) string {
	s := &q.saved
	if s.confirm == confirmOverwrite {
		var b strings.Builder
		b.WriteString(stWarn.Render(clip(fmt.Sprintf("%q already exists", s.name), width)) + "\n\n")
		b.WriteString("Overwrite it?\n")
		b.WriteString(stOK.Render("y") + " overwrite   " + stErr.Render("n/esc") + " keep the old one")
		return stModal.Render(b.String())
	}
	var b strings.Builder
	b.WriteString(stSection.Render("Save query") + "\n\n")
	b.WriteString("name:  " + clip(s.name, width) + stSelected.Render(" ") + "\n")
	b.WriteString("scope: " + stBadge.Render(q.saveScopeLabel()) + "\n")
	if s.errMsg != "" {
		b.WriteString(stErr.Render(clip(s.errMsg, width)) + "\n")
	}
	b.WriteString("\n" + stDim.Render("tab scope · enter save · esc back"))
	return stModal.Render(b.String())
}

// saveScopeLabel says where the entry being saved will live.
func (q *queryModel) saveScopeLabel() string {
	if q.saved.scope == snippets.ScopeConnection && q.connName != "" {
		return q.connName + " only"
	}
	return "global"
}

// savedConfirmView asks before deleting the highlighted entry.
func (q *queryModel) savedConfirmView(width int) string {
	name := ""
	if e, ok := q.selectedSnippet(); ok {
		name = e.Name
	}
	var b strings.Builder
	b.WriteString(stWarn.Render(clip(fmt.Sprintf("Delete %q?", name), width)) + "\n\n")
	b.WriteString(stOK.Render("y") + " delete   " + stErr.Render("n/esc") + " keep")
	return stModal.Render(b.String())
}
