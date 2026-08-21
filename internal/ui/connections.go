package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
	"github.com/andreim/d9s/internal/secrets"
	"github.com/andreim/d9s/internal/sshtunnel"
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

	driver   db.Driver // connection-level session (database listing)
	tunnel   *sshtunnel.Tunnel
	password string // resolved secret, cached after first successful connect
	dbs      []db.Database
}

type connResultMsg struct {
	idx      int
	driver   db.Driver
	dbs      []db.Database
	password string
	err      error
}

const connectTimeout = 60 * time.Second

func (m *model) updateConnections(msg tea.KeyMsg) tea.Cmd {
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

// connectCmd resolves the password, connects the engine driver and lists the
// databases, all off the UI goroutine.
func connectCmd(res *secrets.Resolver, conn config.Connection, tun *sshtunnel.Tunnel, idx int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		pw, err := res.Resolve(ctx, conn.Password)
		if err != nil {
			return connResultMsg{idx: idx, err: err}
		}
		drv, err := db.New(conn.Type)
		if err != nil {
			return connResultMsg{idx: idx, err: err}
		}
		t := db.Target{Config: conn, Password: pw, Secrets: res}
		if tun != nil {
			t.Dial = tun.Dial
		}
		if err := drv.Connect(ctx, t); err != nil {
			_ = drv.Close()
			return connResultMsg{idx: idx, err: err}
		}
		dbs, err := drv.ListDatabases(ctx)
		if err != nil {
			_ = drv.Close()
			return connResultMsg{idx: idx, err: err}
		}
		return connResultMsg{idx: idx, driver: drv, dbs: dbs, password: pw}
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
		m.status = stErr.Render(fmt.Sprintf("%s: %s", cs.cfg.Name, cs.errMsg))
		return
	}
	cs.status = statusConnected
	cs.errMsg = ""
	cs.driver = msg.driver
	cs.dbs = msg.dbs
	cs.password = msg.password
	if m.view == viewConnections {
		m.enterDatabases(msg.idx)
	}
}

func (m *model) connectionsView() string {
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
	b.WriteString("Create " + stHeader.Render(m.cfgPath) + " with entries like:\n\n")
	b.WriteString(stDim.Render(config.Sample))
	return b.String()
}
