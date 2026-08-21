package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/export"
)

// exportDoneMsg reports the outcome of writing a result to a file.
type exportDoneMsg struct {
	path   string
	format export.Format
	rows   int
	err    error
}

// clipboardDoneMsg reports the outcome of a clipboard copy.
type clipboardDoneMsg struct {
	rows int
	err  error
}

// defaultExportPath is the path offered for the n-th statement's result, where
// n is the 1-based index shown in the results area.
func defaultExportPath(n int) string {
	return fmt.Sprintf("./results-%d.csv", n)
}

// focusedResult returns the result the user is on in the results area together
// with its 1-based index, or false when there is nothing to act on.
func (q *queryModel) focusedResult() (db.Result, int, bool) {
	if q.resSel < 0 || q.resSel >= len(q.results) {
		return db.Result{}, 0, false
	}
	return q.results[q.resSel], q.resSel + 1, true
}

// exportBlocker explains why a result cannot be exported, or returns "" when
// it can be.
func exportBlocker(res db.Result) string {
	switch {
	case res.Skipped:
		return "statement was skipped – nothing to export"
	case res.Err != nil:
		return "statement failed – nothing to export"
	case len(res.Columns) == 0:
		return "statement returned no result set to export"
	}
	return ""
}

// openExportPrompt starts the filename prompt for the focused result.
func (m *model) openExportPrompt() tea.Cmd {
	q := &m.query
	res, n, ok := q.focusedResult()
	if !ok {
		m.status = "no result to export"
		return nil
	}
	if why := exportBlocker(res); why != "" {
		m.status = stWarn.Render(why)
		return nil
	}
	q.exportPrompt = true
	q.exportPath = defaultExportPath(n)
	q.exportIdx = n
	q.exportRes = res
	return nil
}

// copyFocusedResult puts the focused result on the clipboard as CSV.
func (m *model) copyFocusedResult() tea.Cmd {
	q := &m.query
	res, _, ok := q.focusedResult()
	if !ok {
		m.status = "no result to copy"
		return nil
	}
	if why := exportBlocker(res); why != "" {
		m.status = stWarn.Render(why)
		return nil
	}
	return copyResultCmd(res)
}

func (q *queryModel) closeExportPrompt() {
	q.exportPrompt = false
	q.exportPath = ""
	q.exportIdx = 0
	q.exportRes = db.Result{}
}

// updateExportPrompt edits the path while the prompt is open: runes type,
// enter writes the file, esc cancels without touching the disk.
func (m *model) updateExportPrompt(msg tea.KeyMsg) tea.Cmd {
	q := &m.query
	switch msg.String() {
	case "esc":
		q.closeExportPrompt()
		m.status = "export cancelled"
		return nil
	case "enter":
		path := strings.TrimSpace(q.exportPath)
		if path == "" {
			m.status = stWarn.Render("enter a file path, or esc to cancel")
			return nil
		}
		res := q.exportRes
		q.closeExportPrompt()
		return writeExportCmd(expandPath(path), res)
	case "backspace":
		if r := []rune(q.exportPath); len(r) > 0 {
			q.exportPath = string(r[:len(r)-1])
		}
		return nil
	case "ctrl+u":
		q.exportPath = ""
		return nil
	}

	switch {
	case msg.Type == tea.KeyRunes && !msg.Alt:
		q.exportPath += string(msg.Runes)
	case msg.Type == tea.KeySpace:
		q.exportPath += " "
	}
	return nil
}

// --- commands -------------------------------------------------------------

func writeExportCmd(path string, res db.Result) tea.Cmd {
	return func() tea.Msg {
		rows, err := export.WriteFile(path, res)
		return exportDoneMsg{path: path, format: export.FormatForPath(path), rows: rows, err: err}
	}
}

func copyResultCmd(res db.Result) tea.Cmd {
	return func() tea.Msg {
		if err := export.Copy(res); err != nil {
			return clipboardDoneMsg{err: err}
		}
		return clipboardDoneMsg{rows: len(res.Rows)}
	}
}

func (m *model) handleExportDone(msg exportDoneMsg) {
	if msg.err != nil {
		m.status = stErr.Render(msg.err.Error())
		return
	}
	m.status = stOK.Render(fmt.Sprintf("wrote %d row(s) to %s (%s)", msg.rows, msg.path, msg.format))
}

func (m *model) handleClipboardDone(msg clipboardDoneMsg) {
	if msg.err != nil {
		m.status = stErr.Render(msg.err.Error())
		return
	}
	m.status = stOK.Render(fmt.Sprintf("copied %d row(s) to the clipboard as CSV", msg.rows))
}

// expandPath expands a leading ~ to the user's home directory; every other
// path is returned unchanged.
func expandPath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// --- rendering ------------------------------------------------------------

// exportPromptView renders the filename prompt for the focused result.
func (q *queryModel) exportPromptView(width int) string {
	limit := min(maxStmtWidth, width-14)
	var b strings.Builder
	b.WriteString(stSection.Render(fmt.Sprintf("Export result [%d]", q.exportIdx)) + "\n")
	b.WriteString(stDim.Render(clip(q.exportRes.Statement, limit)) + "\n\n")
	b.WriteString("path: " + clip(q.exportPath, limit) + stSelected.Render(" ") + "\n")
	b.WriteString(stDim.Render(fmt.Sprintf("format: %s · %d row(s)",
		export.FormatForPath(q.exportPath), len(q.exportRes.Rows))) + "\n\n")
	b.WriteString(stOK.Render("enter") + " write   " + stErr.Render("esc") + " cancel")
	return stModal.Render(b.String())
}
