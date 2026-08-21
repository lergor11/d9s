package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
)

// newResultsModel returns a model in the query view with the results pane
// focused and the given results already rendered.
func newResultsModel(t *testing.T, results []db.Result) *model {
	t.Helper()
	m := &model{view: viewQuery, activeConn: -1}
	m.query.open(nil, config.Postgres, "prod-pg", "app")
	m.query.layout(80, 24)
	m.query.results = results
	m.query.focusResults = true
	m.query.renderResults()
	return m
}

func sampleResults() []db.Result {
	return []db.Result{
		{
			Statement: "SELECT * FROM users",
			Columns:   []string{"id", "email"},
			Rows:      [][]string{{"1", "a@example.com"}, {"2", "b@example.com"}},
			Affected:  -1,
		},
		{
			Statement: "SELECT count(*) FROM orders",
			Columns:   []string{"count"},
			Rows:      [][]string{{"7"}},
			Affected:  -1,
		},
	}
}

// typePath feeds a path into the open prompt one key at a time.
func typePath(m *model, path string) {
	for _, r := range path {
		if r == ' ' {
			m.updateQuery(key("space"))
			continue
		}
		m.updateQuery(key(string(r)))
	}
}

func TestDefaultExportPath(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{name: "first statement", n: 1, want: "./results-1.csv"},
		{name: "second statement", n: 2, want: "./results-2.csv"},
		{name: "tenth statement", n: 10, want: "./results-10.csv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultExportPath(tt.n); got != tt.want {
				t.Errorf("defaultExportPath(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestExportPromptDefaultsToFocusedResult(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string
	}{
		{name: "first result", want: "./results-1.csv"},
		{name: "after j", keys: []string{"j"}, want: "./results-2.csv"},
		{name: "j past the end clamps", keys: []string{"j", "j", "j"}, want: "./results-2.csv"},
		{name: "j then k returns", keys: []string{"j", "k"}, want: "./results-1.csv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newResultsModel(t, sampleResults())
			for _, k := range tt.keys {
				m.updateQuery(key(k))
			}
			m.updateQuery(key("e"))
			if !m.query.exportPrompt {
				t.Fatal("`e` did not open the export prompt")
			}
			if got := m.query.exportPath; got != tt.want {
				t.Errorf("prompt path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExportWritesFocusedResult(t *testing.T) {
	tests := []struct {
		name     string
		move     []string
		file     string
		wantHead string
		wantRows int
	}{
		{
			name:     "first result as csv",
			file:     "out.csv",
			wantHead: "id,email",
			wantRows: 2,
		},
		{
			name:     "second result as csv",
			move:     []string{"j"},
			file:     "second.csv",
			wantHead: "count",
			wantRows: 1,
		},
		{
			name:     "extension picks json",
			file:     "out.json",
			wantHead: `"email": "a@example.com"`,
			wantRows: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			m := newResultsModel(t, sampleResults())
			for _, k := range tt.move {
				m.updateQuery(key(k))
			}
			m.updateQuery(key("e"))
			m.updateQuery(key("ctrl+u")) // clear the default
			typePath(m, path)

			cmd := m.updateQuery(key("enter"))
			if cmd == nil {
				t.Fatal("enter did not start a write")
			}
			if m.query.exportPrompt {
				t.Error("prompt still open after enter")
			}
			msg, ok := cmd().(exportDoneMsg)
			if !ok {
				t.Fatal("write command did not produce an exportDoneMsg")
			}
			if msg.err != nil {
				t.Fatalf("export failed: %v", msg.err)
			}
			m.handleExportDone(msg)

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if !strings.Contains(string(raw), tt.wantHead) {
				t.Errorf("file does not contain %q:\n%s", tt.wantHead, raw)
			}
			if msg.rows != tt.wantRows {
				t.Errorf("rows written = %d, want %d", msg.rows, tt.wantRows)
			}
			if !strings.Contains(m.status, path) {
				t.Errorf("status = %q, want it to name the file", m.status)
			}
		})
	}
}

func TestExportPromptCancelLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cancelled.csv")
	m := newResultsModel(t, sampleResults())

	m.updateQuery(key("e"))
	m.updateQuery(key("ctrl+u"))
	typePath(m, path)
	if cmd := m.updateQuery(key("esc")); cmd != nil {
		t.Error("esc returned a command, want the write skipped")
	}
	if m.query.exportPrompt {
		t.Error("prompt still open after esc")
	}
	if m.query.exportPath != "" {
		t.Errorf("prompt path = %q, want it cleared", m.query.exportPath)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(%q) = %v, want the file never created", path, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("directory holds %d entries, want none", len(entries))
	}
	if m.status == "" {
		t.Error("cancelling produced no status message")
	}
}

func TestExportPromptEditing(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string
	}{
		{name: "typing appends", keys: []string{"a", "b"}, want: "./results-1.csvab"},
		{name: "backspace deletes", keys: []string{"backspace", "backspace", "backspace"}, want: "./results-1."},
		{name: "ctrl+u clears", keys: []string{"ctrl+u"}, want: ""},
		{name: "space is typed", keys: []string{"ctrl+u", "a", "space", "b"}, want: "a b"},
		{name: "retype after clearing", keys: []string{"ctrl+u", "x", ".", "j", "s", "o", "n"}, want: "x.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newResultsModel(t, sampleResults())
			m.updateQuery(key("e"))
			for _, k := range tt.keys {
				m.updateQuery(key(k))
			}
			if got := m.query.exportPath; got != tt.want {
				t.Errorf("prompt path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExportEmptyPathKeepsPromptOpen(t *testing.T) {
	m := newResultsModel(t, sampleResults())
	m.updateQuery(key("e"))
	m.updateQuery(key("ctrl+u"))
	if cmd := m.updateQuery(key("enter")); cmd != nil {
		t.Error("enter on an empty path started a write")
	}
	if !m.query.exportPrompt {
		t.Error("prompt closed on an empty path")
	}
	if m.status == "" {
		t.Error("no status message asking for a path")
	}
}

func TestExportRefusals(t *testing.T) {
	tests := []struct {
		name    string
		results []db.Result
		key     string
	}{
		{name: "export with no results", results: nil, key: "e"},
		{name: "copy with no results", results: nil, key: "y"},
		{
			name:    "export a failed statement",
			results: []db.Result{{Statement: "SELECT boom", Err: errors.New("syntax error"), Affected: -1}},
			key:     "e",
		},
		{
			name:    "export a skipped statement",
			results: []db.Result{{Statement: "SELECT 1", Skipped: true, Affected: -1}},
			key:     "e",
		},
		{
			name:    "export a statement with no result set",
			results: []db.Result{{Statement: "CREATE TABLE t (id int)", Affected: 0}},
			key:     "e",
		},
		{
			name:    "copy a failed statement",
			results: []db.Result{{Statement: "SELECT boom", Err: errors.New("syntax error"), Affected: -1}},
			key:     "y",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newResultsModel(t, tt.results)
			if cmd := m.updateQuery(key(tt.key)); cmd != nil {
				t.Error("a command was dispatched for a result that cannot be exported")
			}
			if m.query.exportPrompt {
				t.Error("the prompt opened for a result that cannot be exported")
			}
			if m.status == "" {
				t.Error("no status message explaining the refusal")
			}
		})
	}
}

func TestExportKeysNeedResultsFocus(t *testing.T) {
	m := newResultsModel(t, sampleResults())
	m.query.focusResults = false
	_ = m.query.ta.Focus()

	for _, k := range []string{"e", "y"} {
		m.updateQuery(key(k))
	}
	if m.query.exportPrompt {
		t.Error("`e` opened the prompt while the editor was focused")
	}
	if got := m.query.ta.Value(); got != "ey" {
		t.Errorf("editor = %q, want the typed %q", got, "ey")
	}
}

// TestCopyBuildsCommand checks that `y` hands back a clipboard command without
// running it — executing it would spawn pbcopy and clobber the real clipboard.
func TestCopyBuildsCommand(t *testing.T) {
	m := newResultsModel(t, sampleResults())
	m.updateQuery(key("j"))
	if cmd := m.updateQuery(key("y")); cmd == nil {
		t.Fatal("`y` returned no clipboard command")
	}

	m.handleClipboardDone(clipboardDoneMsg{rows: 7})
	if !strings.Contains(m.status, "7 row(s)") {
		t.Errorf("status = %q, want the copied row count", m.status)
	}
	m.handleClipboardDone(clipboardDoneMsg{err: errors.New("no clipboard tool found")})
	if !strings.Contains(m.status, "no clipboard tool found") {
		t.Errorf("status = %q, want the clipboard error surfaced as-is", m.status)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "relative path untouched", path: "./results-1.csv", want: "./results-1.csv"},
		{name: "absolute path untouched", path: "/tmp/out.csv", want: "/tmp/out.csv"},
		{name: "tilde expands", path: "~/out.csv", want: filepath.Join(home, "out.csv")},
		{name: "bare tilde expands", path: "~", want: home},
		{name: "tilde inside a name is literal", path: "a~b.csv", want: "a~b.csv"},
		{name: "other user's tilde is literal", path: "~root/out.csv", want: "~root/out.csv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandPath(tt.path); got != tt.want {
				t.Errorf("expandPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestResultSelectionRendering(t *testing.T) {
	m := newResultsModel(t, sampleResults())
	if got := len(m.query.resOffsets); got != 2 {
		t.Fatalf("recorded %d section offsets, want 2", got)
	}
	if m.query.resOffsets[0] != 0 {
		t.Errorf("first section starts at line %d, want 0", m.query.resOffsets[0])
	}
	if m.query.resOffsets[1] <= m.query.resOffsets[0] {
		t.Errorf("section offsets %v are not increasing", m.query.resOffsets)
	}

	// The cursor marks the focused statement and follows j.
	content := m.query.resultsContent()
	first := strings.Split(content, "\n")[0]
	if !strings.HasPrefix(first, "> ") {
		t.Errorf("first section is not marked as focused: %q", first)
	}
	m.updateQuery(key("j"))
	lines := strings.Split(m.query.resultsContent(), "\n")
	if strings.HasPrefix(lines[0], "> ") {
		t.Errorf("first section still marked after moving down: %q", lines[0])
	}
	if second := lines[m.query.resOffsets[1]]; !strings.HasPrefix(second, "> ") {
		t.Errorf("second section is not marked after moving down: %q", second)
	}

	// With the editor focused there is no statement cursor at all.
	m.query.focusResults = false
	if strings.Contains(m.query.resultsContent(), "> ") {
		t.Error("statement cursor rendered while the editor was focused")
	}
}

func TestResultSelectionSurvivesStreaming(t *testing.T) {
	m := newResultsModel(t, nil)
	m.query.ch = make(chan db.Result, 1)
	for _, res := range sampleResults() {
		m.handleExecMsg(execResultMsg{res: res})
	}
	if got := m.query.resSel; got != 0 {
		t.Errorf("selection = %d, want it to stay on the first statement", got)
	}

	m.updateQuery(key("j"))
	if got := m.query.resSel; got != 1 {
		t.Fatalf("selection = %d, want 1", got)
	}
	// A fresh run clears the results and the selection with them.
	m.query.results = nil
	m.query.resSel = 0
	if _, _, ok := m.query.focusedResult(); ok {
		t.Error("focusedResult reported a result after the list was cleared")
	}
}

func TestExportPromptView(t *testing.T) {
	m := newResultsModel(t, sampleResults())
	m.updateQuery(key("e"))
	out := m.query.exportPromptView(80)
	for _, want := range []string{"Export result [1]", "./results-1.csv", "csv", "esc", "2 row(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt does not mention %q:\n%s", want, out)
		}
	}

	m.updateQuery(key("ctrl+u"))
	typePath(m, "out.json")
	if out := m.query.exportPromptView(80); !strings.Contains(out, "json") {
		t.Errorf("prompt does not reflect the json extension:\n%s", out)
	}
}

// TestExportMsgRouting checks the messages reach their handlers through the
// root Update, the way the event loop delivers them.
func TestExportMsgRouting(t *testing.T) {
	m := newResultsModel(t, sampleResults())
	var msgs []tea.Msg
	msgs = append(msgs,
		exportDoneMsg{path: "./out.csv", format: "csv", rows: 3},
		clipboardDoneMsg{rows: 3},
	)
	for _, msg := range msgs {
		updated, cmd := m.Update(msg)
		if cmd != nil {
			t.Errorf("%T produced a command, want none", msg)
		}
		if updated != tea.Model(m) {
			t.Errorf("%T replaced the model", msg)
		}
		if m.status == "" {
			t.Errorf("%T produced no status message", msg)
		}
		m.status = ""
	}
}
