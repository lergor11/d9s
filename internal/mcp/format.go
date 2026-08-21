package mcp

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The caps that bound every tool response. They exist so a `SELECT *` against
// a large table costs the agent a screenful of context rather than its whole
// window.
const (
	// MaxRows is the number of rows one response may carry.
	MaxRows = 200
	// MaxBytes is the size one response may reach.
	MaxBytes = 100 << 10
	// maxCellBytes bounds a single rendered value, so one blob column cannot
	// consume the whole response budget by itself.
	maxCellBytes = 2 << 10
	// noticeReserve is the room held back from MaxBytes for the truncation
	// notices, which must survive the cut that produced them.
	noticeReserve = 512
)

// cutMarker ends a value or a response that was cut to fit a cap.
const cutMarker = "…(cut)"

// grid renders rows the caller holds in full, so a truncation notice can name
// the total the agent is missing out of.
func grid(columns []string, rows [][]string) string {
	return renderGrid(columns, rows, len(rows), false)
}

// gridPaged renders one page of a streamed result. The total is unknown by
// design — counting it would mean reading the rows the cap exists to avoid —
// so more carries what the cursor knows instead: that further rows remain.
func gridPaged(columns []string, rows [][]string, more bool) string {
	return renderGrid(columns, rows, -1, more)
}

// renderGrid renders a result as an aligned text table bounded by the row,
// cell and byte caps, followed by a notice for every cap that fired. A notice
// always states how many rows the agent is actually looking at, and out of how
// many when total says; a negative total means only that more remain.
func renderGrid(columns []string, rows [][]string, total int, more bool) string {
	if len(rows) == 0 {
		return "(no rows)"
	}
	capped := rows
	if len(rows) > MaxRows {
		capped = rows[:MaxRows]
		more = true
	}

	// Values are cut before the widths are measured, so one oversized cell
	// cannot stretch its column across the whole table.
	cells := make([][]string, len(capped))
	cut := 0
	for i, row := range capped {
		cells[i] = make([]string, len(row))
		for j, value := range row {
			text, wasCut := cutBytes(value, maxCellBytes)
			if wasCut {
				cut++
			}
			cells[i][j] = text
		}
	}

	widths := make([]int, len(columns))
	for i, name := range columns {
		widths[i] = utf8.RuneCountInString(name)
	}
	for _, row := range cells {
		for i, value := range row {
			if i < len(widths) && utf8.RuneCountInString(value) > widths[i] {
				widths[i] = utf8.RuneCountInString(value)
			}
		}
	}

	var b strings.Builder
	if len(columns) > 0 {
		b.WriteString(line(columns, widths))
		b.WriteString(rule(widths))
	}
	budget := MaxBytes - noticeReserve
	shown := 0
	for _, row := range cells {
		text := line(row, widths)
		if b.Len()+len(text) > budget {
			break
		}
		b.WriteString(text)
		shown++
	}

	var reasons []string
	if more {
		reasons = append(reasons, fmt.Sprintf("the %d-row cap", MaxRows))
	}
	if shown < len(cells) {
		reasons = append(reasons, fmt.Sprintf("the %d-byte response cap", MaxBytes))
	}
	if len(reasons) > 0 {
		count := fmt.Sprintf("%d rows shown and more remain", shown)
		if total >= 0 {
			count = fmt.Sprintf("%d of %d rows shown", shown, total)
		}
		fmt.Fprintf(&b, "\nTruncated by %s: %s. Narrow the query with a WHERE clause or a LIMIT to see the rest.\n",
			strings.Join(reasons, " and "), count)
	}
	if cut > 0 {
		fmt.Fprintf(&b, "Values over the %d-byte cell cap were cut (%d of them); a cut value ends with %q.\n", maxCellBytes, cut, cutMarker)
	}

	// A backstop, in case the header alone overran the budget.
	out, _ := cutBytes(b.String(), MaxBytes)
	return out
}

// line renders one row padded to the column widths. The last column is not
// padded, so a wide result does not carry a tail of spaces on every row.
func line(row []string, widths []int) string {
	var b strings.Builder
	for i, value := range row {
		if i > 0 {
			b.WriteString(" | ")
		}
		if i < len(row)-1 && i < len(widths) {
			b.WriteString(value)
			b.WriteString(strings.Repeat(" ", max(0, widths[i]-utf8.RuneCountInString(value))))
			continue
		}
		b.WriteString(value)
	}
	b.WriteString("\n")
	return b.String()
}

// rule renders the divider under the header row.
func rule(widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat("-", w)
	}
	return strings.Join(parts, "-+-") + "\n"
}

// cutBytes shortens s to at most limit bytes, cutting on a rune boundary so
// the result is never left holding half a character, and reports whether it
// cut anything.
func cutBytes(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	marker := cutMarker
	if limit < len(marker) {
		marker = "" // no room to say it was cut, but the cap still holds
	}
	keep := limit - len(marker)
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + marker, true
}

// heading opens the block reporting on one statement.
func heading(statement string) string {
	return fmt.Sprintf("-- %s\n", oneLine(statement))
}

// formatSkipped reports a statement that never ran.
func formatSkipped(statement string) string {
	return heading(statement) + "Skipped: an earlier statement in the same call failed.\n"
}

// formatFailure reports a statement the engine rejected.
func formatFailure(statement string, err error) string {
	return heading(statement) + fmt.Sprintf("Failed: %s\n", err)
}

// formatPage renders one page of a statement's result: the rows it returned,
// or the rows it affected when it returned none. more reports that the cursor
// stopped at the row cap with rows left behind.
func formatPage(statement string, columns []string, rows [][]string, more bool, affected int64, elapsed time.Duration) string {
	b := strings.Builder{}
	b.WriteString(heading(statement))
	switch {
	case len(columns) > 0:
		b.WriteString(gridPaged(columns, rows, more))
	case affected >= 0:
		fmt.Fprintf(&b, "OK: %s affected in %s.\n", countRows(affected), elapsed.Round(time.Millisecond))
	default:
		fmt.Fprintf(&b, "OK in %s.\n", elapsed.Round(time.Millisecond))
	}
	return b.String()
}

// countRows names a row count in words that read correctly at one row.
func countRows(n int64) string {
	if n == 1 {
		return "1 row"
	}
	return fmt.Sprintf("%d rows", n)
}

// oneLine collapses a statement onto a single line for a result heading, so a
// formatted multi-line query does not bury the result it belongs to.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	text, _ := cutBytes(s, 200)
	return text
}
