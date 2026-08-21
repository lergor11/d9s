package ui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/history"
)

// key turns a key name into the KeyMsg the terminal would produce.
func key(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+h":
		return tea.KeyMsg{Type: tea.KeyCtrlH}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

// newQueryModel returns a model sitting in the query view with the given
// history entries already loaded and the overlay open.
func newQueryModel(t *testing.T, entries []history.Entry) *model {
	t.Helper()
	m := &model{view: viewQuery, activeConn: -1}
	m.query.open(nil, config.Postgres, "prod-pg", "app")
	m.query.layout(80, 24)
	m.query.histOpen = true
	m.query.histAll = entries
	m.query.applyHistFilter()
	return m
}

func sampleEntries() []history.Entry {
	now := time.Now()
	return []history.Entry{
		{Timestamp: now, Statement: "SELECT * FROM users", Connection: "prod-pg", Database: "app", OK: true},
		{Timestamp: now.Add(-time.Minute), Statement: "DELETE FROM user_sessions", Connection: "prod-pg", Database: "app", OK: false},
		{Timestamp: now.Add(-time.Hour), Statement: "SHOW TABLES", Connection: "prod-pg", Database: "app", OK: true},
	}
}

func TestHistoryOverlayKeys(t *testing.T) {
	tests := []struct {
		name       string
		keys       []string
		wantOpen   bool
		wantFilter string
		wantShown  []string
		wantEditor string
	}{
		{
			name:      "opens on the newest entry",
			wantOpen:  true,
			wantShown: []string{"SELECT * FROM users", "DELETE FROM user_sessions", "SHOW TABLES"},
		},
		{
			name:      "j and k move the selection while the filter is empty",
			keys:      []string{"j", "j", "k"},
			wantOpen:  true,
			wantShown: []string{"SELECT * FROM users", "DELETE FROM user_sessions", "SHOW TABLES"},
		},
		{
			name:       "typing filters case-insensitively",
			keys:       []string{"u", "s", "e", "r"},
			wantOpen:   true,
			wantFilter: "user",
			wantShown:  []string{"SELECT * FROM users", "DELETE FROM user_sessions"},
		},
		{
			name:       "space is part of the filter",
			keys:       []string{"f", "r", "o", "m", "space", "u"},
			wantOpen:   true,
			wantFilter: "from u",
			wantShown:  []string{"SELECT * FROM users", "DELETE FROM user_sessions"},
		},
		{
			name:       "backspace widens the filter again",
			keys:       []string{"s", "h", "o", "backspace", "backspace"},
			wantOpen:   true,
			wantFilter: "s",
			wantShown:  []string{"SELECT * FROM users", "DELETE FROM user_sessions", "SHOW TABLES"},
		},
		{
			name:       "ctrl+u clears the filter",
			keys:       []string{"s", "h", "o", "ctrl+u"},
			wantOpen:   true,
			wantFilter: "",
			wantShown:  []string{"SELECT * FROM users", "DELETE FROM user_sessions", "SHOW TABLES"},
		},
		{
			name:       "j types into a non-empty filter",
			keys:       []string{"u", "j"},
			wantOpen:   true,
			wantFilter: "uj",
			wantShown:  nil,
		},
		{
			name:       "enter inserts the selected statement without running it",
			keys:       []string{"down", "enter"},
			wantOpen:   false,
			wantEditor: "DELETE FROM user_sessions",
		},
		{
			name:       "filter then enter inserts the match",
			keys:       []string{"t", "a", "b", "l", "e", "enter"},
			wantOpen:   false,
			wantEditor: "SHOW TABLES",
		},
		{
			name:       "esc closes without touching the editor",
			keys:       []string{"u", "esc"},
			wantOpen:   false,
			wantEditor: "",
		},
		{
			name:       "ctrl+h closes the overlay too",
			keys:       []string{"ctrl+h"},
			wantOpen:   false,
			wantEditor: "",
		},
		{
			name:       "enter with no match closes without inserting",
			keys:       []string{"z", "z", "z", "enter"},
			wantOpen:   false,
			wantEditor: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newQueryModel(t, sampleEntries())
			for _, k := range tt.keys {
				m.updateQuery(key(k))
			}
			q := &m.query
			if q.histOpen != tt.wantOpen {
				t.Errorf("histOpen = %v, want %v", q.histOpen, tt.wantOpen)
			}
			if q.ta.Value() != tt.wantEditor {
				t.Errorf("editor = %q, want %q", q.ta.Value(), tt.wantEditor)
			}
			if !tt.wantOpen {
				return
			}
			if q.histFilter != tt.wantFilter {
				t.Errorf("histFilter = %q, want %q", q.histFilter, tt.wantFilter)
			}
			var shown []string
			for _, e := range q.histShown {
				shown = append(shown, e.Statement)
			}
			if !reflect.DeepEqual(shown, tt.wantShown) {
				t.Errorf("visible entries = %#v, want %#v", shown, tt.wantShown)
			}
		})
	}
}

func TestHistorySelectionMovement(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want int
	}{
		{name: "starts at the newest entry", want: 0},
		{name: "j moves down", keys: []string{"j"}, want: 1},
		{name: "k stops at the top", keys: []string{"k", "k"}, want: 0},
		{name: "down stops at the bottom", keys: []string{"down", "down", "down", "down"}, want: 2},
		{name: "up walks back", keys: []string{"down", "down", "up"}, want: 1},
		{name: "filtering resets the selection", keys: []string{"down", "down", "s"}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newQueryModel(t, sampleEntries())
			for _, k := range tt.keys {
				m.updateQuery(key(k))
			}
			if m.query.histSel != tt.want {
				t.Errorf("histSel = %d, want %d", m.query.histSel, tt.want)
			}
		})
	}
}

func TestHistoryOpenLoadsStore(t *testing.T) {
	store := history.New(filepath.Join(t.TempDir(), "history.jsonl"))
	for _, stmt := range []string{"SELECT 1", "SELECT 2", "SELECT 1"} {
		if err := store.Append(history.Entry{Timestamp: time.Now(), Statement: stmt, Connection: "prod-pg", Database: "app", OK: true}); err != nil {
			t.Fatalf("Append(%q) error = %v", stmt, err)
		}
	}

	m := &model{view: viewQuery, activeConn: -1, hist: store}
	m.query.open(nil, config.Postgres, "prod-pg", "app")
	m.query.layout(80, 24)

	cmd := m.updateQuery(key("ctrl+h"))
	if cmd == nil {
		t.Fatal("ctrl+h returned no command, want a history load")
	}
	if !m.query.histOpen || !m.query.histLoading {
		t.Fatalf("overlay state after ctrl+h: open=%v loading=%v, want both true", m.query.histOpen, m.query.histLoading)
	}
	msg, ok := cmd().(historyLoadedMsg)
	if !ok {
		t.Fatal("history command did not produce a historyLoadedMsg")
	}
	m.handleHistoryLoaded(msg)

	if m.query.histLoading {
		t.Error("histLoading still set after the load message")
	}
	var got []string
	for _, e := range m.query.histShown {
		got = append(got, e.Statement)
	}
	if want := []string{"SELECT 1", "SELECT 2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("loaded entries = %#v, want %#v", got, want)
	}

	m.updateQuery(key("enter"))
	if m.query.histOpen {
		t.Error("overlay still open after enter")
	}
	if got := m.query.ta.Value(); got != "SELECT 1" {
		t.Errorf("editor = %q, want %q", got, "SELECT 1")
	}
}

func TestHistoryUnavailableReportsStatus(t *testing.T) {
	m := &model{view: viewQuery, activeConn: -1, histErr: "no home directory"}
	m.query.open(nil, config.Postgres, "prod-pg", "app")
	if cmd := m.updateQuery(key("ctrl+h")); cmd != nil {
		t.Error("ctrl+h returned a command without a history store")
	}
	if m.query.histOpen {
		t.Error("overlay opened without a history store")
	}
	if m.status == "" {
		t.Error("no status message explaining why history is unavailable")
	}
}

func TestHistoryViewRenders(t *testing.T) {
	m := newQueryModel(t, sampleEntries())
	out := m.query.historyView(80, 24)
	for _, want := range []string{"Query history", "SELECT * FROM users", "esc close"} {
		if !strings.Contains(out, want) {
			t.Errorf("history overlay does not mention %q:\n%s", want, out)
		}
	}

	empty := newQueryModel(t, nil)
	if out := empty.query.historyView(80, 24); !strings.Contains(out, "no history yet") {
		t.Errorf("empty overlay does not explain itself:\n%s", out)
	}

	loading := newQueryModel(t, nil)
	loading.query.histLoading = true
	if out := loading.query.historyView(80, 24); !strings.Contains(out, "loading") {
		t.Errorf("loading overlay does not say so:\n%s", out)
	}
}
