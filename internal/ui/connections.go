package ui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/export"
	"github.com/lergor11/d9s/internal/secrets"
	"github.com/lergor11/d9s/internal/session"
	"github.com/lergor11/d9s/internal/sshtunnel"
)

type connStatus int

const (
	statusDisconnected connStatus = iota
	statusConnecting
	statusConnected
	statusError
)

// connState is the live state of one configured connection. The driver and
// tunnel are kept for reuse when the user re-enters the connection.
type connState struct {
	cfg    config.Connection
	status connStatus
	errMsg string

	driver db.Driver // connection-level session (database listing)
	tunnel *sshtunnel.Tunnel
	dbs    []db.Database
}

type connResultMsg struct {
	idx    int
	driver db.Driver
	dbs    []db.Database
	err    error
}

const connectTimeout = 60 * time.Second

func (m *model) updateConnections(msg tea.KeyMsg) tea.Cmd {
	// The editor overlays this view and takes every key while it is open,
	// including the ones the list would otherwise treat as commands.
	if m.editor != nil {
		return m.updateEditor(msg)
	}
	if m.errDetail != nil {
		switch msg.String() {
		case "esc", "q", "enter", "v":
			m.errDetail = nil
		case "y":
			text := m.errDetail.errMsg
			return func() tea.Msg { return cellCopiedMsg{err: export.CopyText(text)} }
		}
		return nil
	}

	switch msg.String() {
	case "q":
		return tea.Quit
	case "j", "down":
		if m.selConn < len(m.conns)-1 {
			m.selConn++
		}
	case "k", "up":
		if m.selConn > 0 {
			m.selConn--
		}
	case "a":
		m.editor = &connEditor{form: newConnForm()}
	case "e":
		if len(m.conns) > 0 {
			m.editor = &connEditor{form: editConnForm(m.conns[m.selConn].cfg)}
		}
	case "d":
		if len(m.conns) > 0 {
			m.editor = &connEditor{confirm: &editorConfirm{
				kind: editorConfirmDelete, name: m.conns[m.selConn].cfg.Name,
			}}
		}
	case "v":
		// The row shows a clipped error, and a driver's failure text routinely
		// runs past it; this is the only way to read the whole thing.
		if len(m.conns) > 0 && m.conns[m.selConn].errMsg != "" {
			m.errDetail = m.conns[m.selConn]
		}
	case "enter":
		if len(m.conns) == 0 {
			return nil
		}
		cs := m.conns[m.selConn]
		switch cs.status {
		case statusConnecting:
			// already in flight
		case statusConnected:
			m.enterDatabases(m.selConn)
		default:
			cs.status = statusConnecting
			cs.errMsg = ""
			if cs.cfg.SSH != nil && cs.tunnel == nil {
				cs.tunnel = sshtunnel.New(*cs.cfg.SSH)
			}
			return tea.Batch(m.spinner.Tick,
				connectCmd(m.resolver, cs.cfg, cs.tunnel, m.selConn))
		}
	}
	return nil
}

// connectCmd opens the connection-level session and lists the databases, all
// off the UI goroutine and inside one message, so connecting stays a single
// round trip for the spinner. The tunnel is borrowed from connState — a failed
// connect must leave it open for the retry.
func connectCmd(res *secrets.Resolver, conn config.Connection, tun *sshtunnel.Tunnel, idx int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		s, err := session.OpenTunnel(ctx, res, conn, "", tun)
		if err != nil {
			return connResultMsg{idx: idx, err: err}
		}
		dbs, err := s.Driver.ListDatabases(ctx)
		if err != nil {
			_ = s.Close()
			return connResultMsg{idx: idx, err: err}
		}
		return connResultMsg{idx: idx, driver: s.Driver, dbs: dbs}
	}
}

func (m *model) handleConnResult(msg connResultMsg) {
	if msg.idx < 0 || msg.idx >= len(m.conns) {
		return
	}
	cs := m.conns[msg.idx]
	if msg.err != nil {
		cs.status = statusError
		cs.errMsg = msg.err.Error()
		// No connection-name prefix: session.Open already names the connection
		// where it matters, and the row shows the error beside the name anyway.
		m.status = stErr.Render(cs.errMsg)
		return
	}
	cs.status = statusConnected
	cs.errMsg = ""
	cs.driver = msg.driver
	cs.dbs = msg.dbs
	if m.view == viewConnections {
		m.enterDatabases(msg.idx)
	}
}

func (m *model) connectionsView() string {
	// The editor is drawn as an overlay over this view rather than as a view of
	// its own, which keeps the connection list the only thing that routes its
	// keys and the only thing that knows it exists.
	if m.editor != nil {
		return m.overlay(m.editorView())
	}
	if len(m.conns) == 0 {
		return m.onboardingView()
	}
	var b strings.Builder
	for _, w := range m.warns {
		b.WriteString(truncate(stWarn.Render(fmt.Sprintf("⚠ %s: %s", w.Connection, w.Message)), m.width) + "\n")
	}
	if len(m.warns) > 0 {
		b.WriteString("\n")
	}

	nameW, hostW, tlsW := 4, 4, 3
	for _, cs := range m.conns {
		if n := len([]rune(cs.cfg.Name)); n > nameW {
			nameW = n
		}
		if n := len(fmt.Sprintf("%s:%d", cs.cfg.Host, cs.cfg.Port)); n > hostW {
			hostW = n
		}
		if n := len(tlsLabel(cs.cfg)); n > tlsW {
			tlsW = n
		}
	}

	head := fmt.Sprintf("  %s %s %s %s %s %s", pad("NAME", nameW), pad("TYPE", 10), pad("HOST", hostW), pad("SSH", 3), pad("TLS", tlsW), "STATUS")
	b.WriteString(truncate(stTableHead.Render(head), m.width) + "\n")

	for i, cs := range m.conns {
		badge := "   "
		if cs.cfg.SSH != nil {
			badge = stBadge.Render("ssh")
		}
		var status string
		switch cs.status {
		case statusDisconnected:
			status = stDim.Render("disconnected")
		case statusConnecting:
			status = m.spinner.View() + " connecting"
		case statusConnected:
			status = stOK.Render("connected")
		case statusError:
			status = stErr.Render("error: " + clip(cs.errMsg, 60))
		}
		cursor := "  "
		name := pad(cs.cfg.Name, nameW)
		if i == m.selConn {
			cursor = stSelected.Render("> ")
			name = stSelected.Render(name)
		}
		row := fmt.Sprintf("%s%s %s %s %s %s %s",
			cursor, name,
			pad(string(cs.cfg.Type), 10),
			pad(fmt.Sprintf("%s:%d", cs.cfg.Host, cs.cfg.Port), hostW),
			badge, tlsBadge(cs.cfg, tlsW), status)
		b.WriteString(truncate(row, m.width) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// tlsLabel names the transport encryption of a connection for the TLS column.
// A require-mode connection is encrypted but authenticates nothing, so it
// reads as "unverified" rather than as its mode name.
func tlsLabel(conn config.Connection) string {
	switch conn.EffectiveTLSMode() {
	case config.TLSRequire:
		return "unverified"
	case config.TLSVerifyCA:
		return string(config.TLSVerifyCA)
	case config.TLSVerifyFull:
		return string(config.TLSVerifyFull)
	default:
		return ""
	}
}

// tlsBadge renders the TLS column, padded before styling so the ANSI escapes
// do not count towards the column width.
func tlsBadge(conn config.Connection, width int) string {
	label := pad(tlsLabel(conn), width)
	switch conn.EffectiveTLSMode() {
	case config.TLSRequire:
		return stWarn.Render(label)
	case config.TLSVerifyCA, config.TLSVerifyFull:
		return stBadge.Render(label)
	default:
		return label
	}
}

func (m *model) onboardingView() string {
	var b strings.Builder
	b.WriteString(stSection.Render("No connections configured") + "\n\n")
	b.WriteString("Press " + stBadge.Render("a") + " to add one; d9s creates " +
		stHeader.Render(m.cfgPath) + " for you.\n\n")
	b.WriteString(stDim.Render("Or write the file yourself, with entries like:") + "\n\n")
	b.WriteString(stDim.Render(config.Sample))
	return b.String()
}

// reloadConfig re-reads the configuration after a write and rebuilds the
// connection list. A connection whose configuration is unchanged keeps its
// live driver and tunnel; one that was edited or removed has its session
// closed, because it no longer describes what is on the other end.
func (m *model) reloadConfig() error {
	cfg, warns, err := config.Load(m.cfgPath)
	if err != nil {
		return err
	}

	previous := make(map[string]*connState, len(m.conns))
	for _, cs := range m.conns {
		previous[cs.cfg.Name] = cs
	}
	activeName := ""
	if m.activeConn >= 0 && m.activeConn < len(m.conns) {
		activeName = m.conns[m.activeConn].cfg.Name
	}

	conns := make([]*connState, len(cfg.Connections))
	for i, c := range cfg.Connections {
		if kept, ok := previous[c.Name]; ok && reflect.DeepEqual(kept.cfg, c) {
			conns[i] = kept
			delete(previous, c.Name)
			continue
		}
		conns[i] = &connState{cfg: c}
	}
	for _, stale := range previous {
		closeConnState(stale)
	}

	m.cfg, m.warns, m.conns = cfg, warns, conns
	m.activeConn = -1
	for i, cs := range conns {
		if cs.cfg.Name == activeName {
			m.activeConn = i
		}
	}
	if m.selConn >= len(conns) {
		m.selConn = len(conns) - 1
	}
	if m.selConn < 0 {
		m.selConn = 0
	}
	return nil
}

// closeConnState releases the driver and tunnel of a connection that is going
// away.
func closeConnState(cs *connState) {
	if cs.driver != nil {
		_ = cs.driver.Close()
	}
	if cs.tunnel != nil {
		_ = cs.tunnel.Close()
	}
}

// capturesKeys reports whether something on screen is consuming keystrokes
// that would otherwise be read as commands — the connection editor's text
// fields, or the query view's own editor and prompts.
func (m *model) capturesKeys() bool {
	if m.editor != nil {
		return true
	}
	return m.view == viewQuery && m.query.capturesKeys()
}

// connectionsHints is the footer legend for the connection list and whatever
// the editor has on top of it.
func (m *model) connectionsHints() string {
	if m.editor == nil {
		if m.errDetail != nil {
			return "y copy · esc close"
		}
		hints := "j/k move · enter connect · a add · e edit · d delete · q quit · ? help"
		if len(m.conns) > 0 && m.conns[m.selConn].errMsg != "" {
			hints = "j/k move · enter retry · v full error · a add · e edit · d delete · q quit"
		}
		return hints
	}
	e := m.editor
	switch {
	case e.busy != "":
		return "working... · esc give up"
	case e.confirm != nil && e.confirm.kind == editorConfirmPlaintext:
		return "y store plaintext · n cancel · p pick from 1Password"
	case e.confirm != nil:
		return "y delete · n cancel"
	case e.picker != nil:
		return "type to filter · ↑/↓ select · enter choose · esc back"
	default:
		return formHints
	}
}

// connectionsHelp writes the connection-list bindings into the help overlay.
func (m *model) connectionsHelp(write func(key, desc string)) {
	write("j/k, ↑/↓", "move selection")
	write("enter", "connect / open databases")
	write("a", "add a connection")
	write("e", "edit the selected connection")
	write("d", "delete the selected connection (asks first)")
	write("v", "read the whole error of a failed connection")
	write("q", "quit")
	write("", "")
	write("in the form:", "")
	write("tab, ↑/↓", "move between fields")
	write("←/→", "change the engine or the TLS mode")
	write("enter, ctrl+s", "save to "+m.cfgPath)
	write("ctrl+t", "test the connection without saving")
	write("ctrl+p", "pick a password from 1Password")
	write("ctrl+k", "check that the op:// reference resolves")
	write("esc", "cancel")
}

// errDetailView shows a failed connection's whole error. Driver failures
// routinely run past the width of a row, and the clipped version usually cuts
// off exactly the part that says what went wrong.
func (m *model) errDetailView(width, height int) string {
	cs := m.errDetail
	body := lipgloss.NewStyle().Width(max(20, width-10)).Render(cs.errMsg)
	var b strings.Builder
	b.WriteString(stSection.Render(cs.cfg.Name+" could not connect") + "\n\n")
	b.WriteString(stErr.Render(body) + "\n\n")
	b.WriteString(stDim.Render("y copy · esc close"))
	return stModal.MaxHeight(height).Render(b.String())
}
