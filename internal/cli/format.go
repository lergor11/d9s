package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/export"
)

// Format is an output encoding for a result set.
type Format string

// The supported output formats.
const (
	// FormatTable is the aligned text table a person reads.
	FormatTable Format = "table"
	// FormatCSV is a header row followed by one line per row.
	FormatCSV Format = "csv"
	// FormatJSON is one array of row objects per result.
	FormatJSON Format = "json"
	// FormatJSONL is one row object per line.
	FormatJSONL Format = "jsonl"
)

// formats lists the accepted values of -o, in the order the help shows them.
var formats = []Format{FormatTable, FormatCSV, FormatJSON, FormatJSONL}

// resolveFormat turns the -o value into a format. An empty value means the
// flag was not given, and the default then depends on where stdout goes:
// a person at a terminal gets the table, a pipe gets machine-readable JSONL.
func resolveFormat(value string, terminal bool) (Format, error) {
	if value == "" {
		if terminal {
			return FormatTable, nil
		}
		return FormatJSONL, nil
	}
	for _, f := range formats {
		if Format(value) == f {
			return f, nil
		}
	}
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = string(f)
	}
	return "", fail(ExitUsage, "unknown output format %q (want %s)", value, strings.Join(names, ", "))
}

// machine reports whether the format is meant for another program, in which
// case stdout carries rows and nothing else.
func (f Format) machine() bool { return f != FormatTable }

// write renders one result on stdout. label names the statement or the listing
// it came from; in the machine-readable formats it goes to stderr, so a
// consumer of stdout sees data only.
func (e env) write(f Format, res db.Result, label string) error {
	if f.machine() {
		if label != "" {
			_, _ = fmt.Fprintln(e.stderr, label)
		}
		if len(res.Columns) == 0 {
			// A statement that returns no result set, such as an INSERT.
			return nil
		}
		if err := export.Write(e.stdout, res, export.Format(f)); err != nil {
			return fail(ExitError, "writing output: %w", err)
		}
		return nil
	}

	if label != "" {
		if _, err := fmt.Fprintln(e.stdout, label); err != nil {
			return fail(ExitError, "writing output: %w", err)
		}
	}
	if len(res.Columns) == 0 {
		if res.Affected >= 0 {
			_, err := fmt.Fprintf(e.stdout, "%d row(s) affected\n", res.Affected)
			return err
		}
		_, err := fmt.Fprintln(e.stdout, "OK")
		return err
	}
	if err := renderTable(e.stdout, res, e.width); err != nil {
		return fail(ExitError, "writing output: %w", err)
	}
	return nil
}

// separate puts a blank line between two rendered results, so the statements
// of a script do not run together on the screen. Only the text table needs it:
// the machine formats stay strictly one record per line.
func (e env) separate(f Format) {
	if f == FormatTable {
		_, _ = fmt.Fprintln(e.stdout)
	}
}

// summarize writes the row count and the timing of a statement to stderr,
// where it never mixes with the data on stdout.
func (e env) summarize(res db.Result) {
	if res.Duration == 0 && len(res.Columns) == 0 {
		return
	}
	_, _ = fmt.Fprintf(e.stderr, "%d row(s) in %s\n", len(res.Rows), res.Duration.Round(time.Millisecond))
}

const (
	// maxCellWidth caps one column of the text table, so a wide text value
	// cannot push every other column off the screen. It matches the grid the
	// interface draws.
	maxCellWidth = 32
	// minCellWidth is as far as the columns shrink to fit a narrow terminal.
	minCellWidth = 8
	// cellGap separates two columns; gapWidth is its width in cells.
	cellGap  = " | "
	gapWidth = 3
)

// renderTable writes an aligned text table. Columns are capped at
// maxCellWidth, and the cap shrinks further when the terminal is too narrow
// for the natural widths. A width of 0 means the width is unknown, and leaves
// the cap at maxCellWidth.
func renderTable(w io.Writer, res db.Result, width int) error {
	natural := make([]int, len(res.Columns))
	for i, col := range res.Columns {
		natural[i] = runeLen(col)
	}
	for _, row := range res.Rows {
		for i, cell := range row {
			if i < len(natural) && runeLen(cell) > natural[i] {
				natural[i] = runeLen(cell)
			}
		}
	}
	limit := fittingLimit(natural, width)
	widths := make([]int, len(natural))
	for i, n := range natural {
		widths[i] = min(n, limit)
	}

	var b strings.Builder
	cells := make([]string, len(res.Columns))
	for i, col := range res.Columns {
		cells[i] = pad(clip(col, widths[i]), widths[i])
	}
	b.WriteString(strings.TrimRight(strings.Join(cells, cellGap), " ") + "\n")
	for i := range cells {
		cells[i] = strings.Repeat("-", widths[i])
	}
	b.WriteString(strings.Join(cells, "-+-") + "\n")
	for _, row := range res.Rows {
		for i := range cells {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			cells[i] = pad(clip(cell, widths[i]), widths[i])
		}
		b.WriteString(strings.TrimRight(strings.Join(cells, cellGap), " ") + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// fittingLimit returns the largest per-column width that keeps a row inside
// the terminal, never below minCellWidth and never above maxCellWidth.
func fittingLimit(natural []int, width int) int {
	limit := maxCellWidth
	if width <= 0 {
		return limit
	}
	for limit > minCellWidth && rowWidth(natural, limit) > width {
		limit--
	}
	return limit
}

// rowWidth is how wide a row is once every column is capped at limit.
func rowWidth(natural []int, limit int) int {
	total := gapWidth * max(0, len(natural)-1)
	for _, n := range natural {
		total += min(n, limit)
	}
	return total
}

func runeLen(s string) int { return len([]rune(s)) }

// clip cuts a cell to w runes, marking the cut with an ellipsis, and collapses
// the whitespace so an embedded newline cannot break the alignment.
func clip(s string, w int) string {
	if strings.ContainsAny(s, " \t\r\n") {
		s = strings.Join(strings.Fields(s), " ")
	}
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
	if n := runeLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
