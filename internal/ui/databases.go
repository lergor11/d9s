package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/secrets"
	"github.com/lergor11/d9s/internal/session"
	"github.com/lergor11/d9s/internal/sshtunnel"
)

type dbOpenResultMsg struct {
	connIdx int
	dbName  string
	driver  db.Driver
	err     error
}

func (m *model) enterDatabases(idx int) {
	m.activeConn = idx
	if m.selDB >= len(m.conns[idx].dbs) {
		m.selDB = 0
	}
	m.view = viewDatabases
}

func (m *model) updateDatabases(msg tea.KeyMsg) tea.Cmd {
	cs := m.conns[m.activeConn]
	switch msg.String() {
	case "esc":
		m.view = viewConnections
	case "j", "down":
		if m.selDB < len(cs.dbs)-1 {
			m.selDB++
		}
	case "k", "up":
		if m.selDB > 0 {
			m.selDB--
		}
	case "enter":
		if len(cs.dbs) == 0 || m.dbOpening != "" {
			return nil
		}
		name := cs.dbs[m.selDB].Name
		m.dbOpening = name
		return tea.Batch(m.spinner.Tick,
			openDBCmd(m.resolver, cs.cfg, cs.tunnel, m.activeConn, name))
	}
	return nil
}

// openDBCmd opens a fresh session scoped to the selected database through the
// connection's shared SSH tunnel, which the session borrows rather than owns.
// The password is re-resolved on each open; the resolver caches it, so only
// the first resolution of an op:// reference is expensive.
func openDBCmd(res *secrets.Resolver, conn config.Connection, tun *sshtunnel.Tunnel, idx int, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		s, err := session.OpenTunnel(ctx, res, conn, name, tun)
		if err != nil {
			return dbOpenResultMsg{connIdx: idx, dbName: name, err: err}
		}
		return dbOpenResultMsg{connIdx: idx, dbName: name, driver: s.Driver}
	}
}

func (m *model) handleDBOpenResult(msg dbOpenResultMsg) tea.Cmd {
	m.dbOpening = ""
	if msg.err != nil {
		m.status = stErr.Render(fmt.Sprintf("open %s: %s", msg.dbName, msg.err))
		return nil
	}
	if m.view != viewDatabases || m.activeConn != msg.connIdx {
		// User navigated away while connecting; drop the session.
		go func() { _ = msg.driver.Close() }()
		return nil
	}
	cs := m.conns[msg.connIdx]
	m.query.open(msg.driver, cs.cfg.Type, cs.cfg.Name, msg.dbName)
	m.query.layout(m.width, m.bodyHeight())
	m.view = viewQuery
	return tea.Batch(m.query.ta.Focus(), refreshTransactionCmd(msg.driver, cs.cfg.Type))
}

func (m *model) databasesView() string {
	cs := m.conns[m.activeConn]
	if len(cs.dbs) == 0 {
		return stDim.Render("no databases visible on this connection")
	}
	var b strings.Builder
	nameW := 4
	for _, d := range cs.dbs {
		if n := len([]rune(d.Name)); n > nameW {
			nameW = n
		}
	}
	head := fmt.Sprintf("  %s %s", pad("DATABASE", nameW), "DETAIL")
	b.WriteString(truncate(stTableHead.Render(head), m.width) + "\n")
	for i, d := range cs.dbs {
		cursor := "  "
		name := pad(d.Name, nameW)
		if i == m.selDB {
			cursor = stSelected.Render("> ")
			name = stSelected.Render(name)
		}
		row := fmt.Sprintf("%s%s %s", cursor, name, stDim.Render(d.Detail))
		b.WriteString(truncate(row, m.width) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
