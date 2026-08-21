// Package ui implements the k9s-style terminal interface: connection list,
// database list, and query view, driven by a single Bubble Tea root model.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/history"
	"github.com/lergor11/d9s/internal/secrets"
	"github.com/lergor11/d9s/internal/snippets"
)

type viewID int

const (
	viewConnections viewID = iota
	viewDatabases
	viewQuery
)

type model struct {
	cfg     *config.Config
	warns   []config.Warning
	cfgPath string
	version string

	width  int
	height int

	view        viewID
	showHelp    bool
	quitConfirm bool
	status      string // transient status-line message

	resolver *secrets.Resolver
	spinner  spinner.Model

	hist    *history.Store // nil when no history location could be determined
	histErr string         // why hist is nil

	snips    *snippets.Store // nil when no saved-query location could be determined
	snipsErr string          // why snips is nil

	conns   []*connState
	selConn int

	// editor is the add/edit/delete overlay over the connection list; nil when
	// the user is not editing a connection.
	editor *connEditor

	// errDetail is the connection whose full failure text is on screen; nil
	// when no such overlay is open.
	errDetail *connState

	activeConn int // index into conns whose databases are listed
	selDB      int
	dbOpening  string // name of the database being opened; "" when idle

	query queryModel
}

// Run starts the TUI and blocks until the user quits.
func Run(cfg *config.Config, warns []config.Warning, cfgPath, version string) error {
	m := newModel(cfg, warns, cfgPath, version)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if fm, ok := final.(*model); ok {
		fm.closeAll()
	}
	return err
}

func newModel(cfg *config.Config, warns []config.Warning, cfgPath, version string) *model {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = stWarn
	conns := make([]*connState, len(cfg.Connections))
	for i, c := range cfg.Connections {
		conns[i] = &connState{cfg: c}
	}
	m := &model{
		cfg:        cfg,
		warns:      warns,
		cfgPath:    cfgPath,
		version:    version,
		resolver:   secrets.NewResolver(),
		spinner:    sp,
		conns:      conns,
		activeConn: -1,
	}
	hist, err := history.Default()
	if err != nil {
		m.histErr = err.Error()
	} else {
		m.hist = hist
	}
	snips, err := snippets.Default()
	if err != nil {
		m.snipsErr = err.Error()
	} else {
		m.snips = snips
	}
	return m
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) closeAll() {
	m.query.close()
	for _, cs := range m.conns {
		if cs.driver != nil {
			_ = cs.driver.Close()
		}
		if cs.tunnel != nil {
			_ = cs.tunnel.Close()
		}
	}
}

func (m *model) spinning() bool {
	if m.editor != nil && m.editor.busy != "" {
		return true
	}
	if m.dbOpening != "" || m.query.running || m.query.schema.inflight {
		return true
	}
	if m.query.cache != nil && m.query.cache.Loading() {
		return true
	}
	for _, cs := range m.conns {
		if cs.status == statusConnecting {
			return true
		}
	}
	return false
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.query.layout(m.width, m.bodyHeight())
		return m, nil

	case spinner.TickMsg:
		if !m.spinning() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case connResultMsg:
		m.handleConnResult(msg)
		return m, nil
	case dbOpenResultMsg:
		return m, m.handleDBOpenResult(msg)
	case execResultMsg, execDoneMsg:
		return m, m.handleExecMsg(msg)
	case historyLoadedMsg:
		m.handleHistoryLoaded(msg)
		return m, nil
	case schemaTablesMsg:
		m.handleSchemaTables(msg)
		return m, nil
	case schemaColumnsMsg:
		m.handleSchemaColumns(msg)
		return m, nil
	case completionSchemaMsg:
		return m, m.handleCompletionSchema(msg)
	case exportDoneMsg:
		m.handleExportDone(msg)
		return m, nil
	case clipboardDoneMsg:
		m.handleClipboardDone(msg)
		return m, nil
	case cellCopiedMsg:
		m.handleCellCopied(msg)
		return m, nil
	case savedLoadedMsg:
		m.handleSavedLoaded(msg)
		return m, nil
	case savedWroteMsg:
		return m, m.handleSavedWrote(msg)
	case connFormMsg:
		return m, m.handleConnFormMsg(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Quit confirmation dialog takes precedence over everything.
	if m.quitConfirm {
		switch key {
		case "y", "Y":
			return m, tea.Quit
		case "n", "N", "esc":
			m.quitConfirm = false
		}
		return m, nil
	}

	if key == "ctrl+c" {
		if m.query.running {
			m.quitConfirm = true
			return m, nil
		}
		return m, tea.Quit
	}

	// Help overlay: any relevant key dismisses it.
	if m.showHelp {
		switch key {
		case "?", "esc", "q", "enter":
			m.showHelp = false
		}
		return m, nil
	}

	// '?' opens help everywhere except where the query view takes the key
	// itself: the editor, the confirmation modal and the history filter.
	if key == "?" && !m.capturesKeys() {
		m.showHelp = true
		return m, nil
	}

	m.status = ""
	switch m.view {
	case viewConnections:
		return m, m.updateConnections(msg)
	case viewDatabases:
		return m, m.updateDatabases(msg)
	case viewQuery:
		return m, m.updateQuery(msg)
	}
	return m, nil
}

// --- layout ---------------------------------------------------------------

// bodyHeight is the space between the header and the status+footer lines.
func (m *model) bodyHeight() int {
	h := m.height - 3
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	var body string
	switch {
	case m.quitConfirm:
		body = m.overlay(m.quitConfirmView())
	case m.showHelp:
		body = m.overlay(m.helpView())
	case m.view == viewConnections && m.errDetail != nil:
		body = m.overlay(m.errDetailView(m.width, m.bodyHeight()))
	case m.view == viewQuery && m.query.confirm != nil:
		body = m.overlay(m.query.confirmView(m.width))
	case m.view == viewQuery && m.query.histOpen:
		body = m.overlay(m.query.historyView(m.width, m.bodyHeight()))
	case m.view == viewQuery && m.query.schema.open:
		body = m.overlay(m.query.schemaView(m.width, m.bodyHeight(), m.spinner.View()))
	case m.view == viewQuery && m.query.exportPrompt:
		body = m.overlay(m.query.exportPromptView(m.width))
	case m.view == viewQuery && m.query.saved.open:
		body = m.overlay(m.query.savedView(m.width, m.bodyHeight()))
	case m.view == viewQuery && m.query.inspect.open:
		body = m.overlay(m.query.inspectView())
	default:
		switch m.view {
		case viewConnections:
			body = m.connectionsView()
		case viewDatabases:
			body = m.databasesView()
		case viewQuery:
			body = m.query.view(m.spinner.View())
		}
	}
	body = lipgloss.NewStyle().Height(m.bodyHeight()).MaxHeight(m.bodyHeight()).Render(body)
	return strings.Join([]string{m.headerView(), body, m.statusView(), m.footerView()}, "\n")
}

func (m *model) overlay(dialog string) string {
	return lipgloss.Place(m.width, m.bodyHeight(), lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) headerView() string {
	title := stHeader.Render("d9s " + m.version)
	var crumbs []string
	if m.view >= viewDatabases && m.activeConn >= 0 {
		cs := m.conns[m.activeConn]
		crumbs = append(crumbs, fmt.Sprintf("%s [%s]", cs.cfg.Name, cs.cfg.Type))
	}
	if m.view == viewQuery && m.query.dbName != "" {
		crumbs = append(crumbs, m.query.dbName)
	}
	line := title
	if len(crumbs) > 0 {
		line += stHeaderDim.Render(" · " + strings.Join(crumbs, " · "))
	}
	return truncate(line, m.width)
}

func (m *model) statusView() string {
	s := m.status
	if s == "" {
		if m.query.running && m.view == viewQuery {
			s = m.spinner.View() + " running... (ctrl+x to cancel)"
		} else if m.dbOpening != "" {
			s = m.spinner.View() + " opening " + m.dbOpening + "..."
		}
	}
	return truncate(stDim.Render(s), m.width)
}

func (m *model) footerView() string {
	var hints string
	switch m.view {
	case viewConnections:
		hints = m.connectionsHints()
	case viewDatabases:
		hints = "j/k move · enter open · esc back · ? help"
	case viewQuery:
		switch {
		case m.query.exportPrompt:
			hints = "type a path · enter write · esc cancel"
		case m.query.histOpen:
			hints = "type to filter · j/k select · enter insert · esc close"
		case m.query.schema.open:
			hints = m.query.schemaHints()
		case m.query.comp.open:
			hints = completionHints
		case m.query.editorFocused():
			hints = "ctrl+r run · tab complete · ctrl+h history · ctrl+s schema · ctrl+j results · esc back"
		default:
			hints = "j/k statement · e export · y copy · s schema · tab editor · esc back · ? help"
		}
	}
	return truncate(stFooter.Render(hints), m.width)
}

func (m *model) quitConfirmView() string {
	return stModal.Render("A query is still running.\n\nQuit anyway?  " +
		stOK.Render("y") + " yes   " + stErr.Render("n") + " no")
}

func (m *model) helpView() string {
	var b strings.Builder
	b.WriteString(stSection.Render("Key bindings") + "\n\n")
	write := func(k, desc string) {
		fmt.Fprintf(&b, "  %-14s %s\n", k, desc)
	}
	switch m.view {
	case viewConnections:
		m.connectionsHelp(write)
	case viewDatabases:
		write("j/k, ↑/↓", "move selection")
		write("enter", "open query view")
		write("esc", "back to connections")
	case viewQuery:
		write("ctrl+r, F5", "run buffer")
		write("alt+enter", "run buffer (shift+enter, if your terminal maps it)")
		write("ctrl+x", "cancel running query")
		write("ctrl+h", "query history (enter inserts, never runs)")
		write("s, ctrl+s", "schema panel (tables → enter → columns, i inserts)")
		write("e", "export focused result to a file (CSV or JSON)")
		write("y", "copy focused result to the clipboard as CSV")
		write("tab", "complete the name at the cursor (ctrl+g reloads names)")
		write("ctrl+j", "toggle editor/results focus (tab, from results)")
		write("j/k", "move between statements (when results focused)")
		write("↑/↓, PgUp/PgDn", "scroll results (when focused)")
		write("esc", "back to databases")
	}
	write("ctrl+c", "quit (confirms if a query is running)")
	write("?", "toggle this help")
	b.WriteString("\n" + stDim.Render("press ? or esc to close"))
	return stHelpBox.Render(b.String())
}

// --- small helpers --------------------------------------------------------

// truncate cuts s to at most w display cells, ANSI-aware via lipgloss.
func truncate(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// clip truncates a plain (unstyled) single-line string to w runes, appending
// an ellipsis when cut.
func clip(s string, w int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if w <= 0 || len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func pad(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}
