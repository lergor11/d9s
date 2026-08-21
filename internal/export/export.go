// Package export renders query results as CSV or JSON and copies them to the
// system clipboard.
package export

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andreim/d9s/internal/db"
)

// Format is an output encoding for a result set.
type Format string

// Supported output formats.
const (
	CSV  Format = "csv"
	JSON Format = "json"
)

// FormatForPath picks the format from a file extension, defaulting to CSV.
func FormatForPath(path string) Format {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return JSON
	}
	return CSV
}

// Write encodes the result in the given format.
func Write(w io.Writer, res db.Result, format Format) error {
	if format == JSON {
		return writeJSON(w, res)
	}
	return writeCSV(w, res)
}

// WriteFile encodes the result to path, choosing the format from its
// extension, and returns the number of data rows written.
func WriteFile(path string, res db.Result) (int, error) {
	f, err := os.Create(path) //nolint:gosec // the path comes from the user's own prompt
	if err != nil {
		return 0, fmt.Errorf("creating %s: %w", path, err)
	}
	if err := Write(f, res, FormatForPath(path)); err != nil {
		_ = f.Close()
		return 0, fmt.Errorf("writing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("closing %s: %w", path, err)
	}
	return len(res.Rows), nil
}

func writeCSV(w io.Writer, res db.Result) error {
	cw := csv.NewWriter(w)
	if len(res.Columns) > 0 {
		if err := cw.Write(res.Columns); err != nil {
			return err
		}
	}
	for _, row := range res.Rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeJSON(w io.Writer, res db.Result) error {
	records := make([]map[string]any, 0, len(res.Rows))
	for _, row := range res.Rows {
		rec := make(map[string]any, len(res.Columns))
		for i, col := range res.Columns {
			if i >= len(row) {
				break
			}
			if row[i] == nullCell {
				rec[col] = nil
				continue
			}
			rec[col] = row[i]
		}
		records = append(records, rec)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(records)
}

// nullCell is how the drivers render a SQL NULL; JSON export maps it back to
// a real null rather than the literal string.
const nullCell = "NULL"

// clipboardCommands are tried in order; the first one present on PATH wins.
var clipboardCommands = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// ErrNoClipboard reports that no supported clipboard tool is installed.
var ErrNoClipboard = errors.New("no clipboard tool found (install pbcopy, wl-copy, xclip, or xsel)")

// Copy places the result on the system clipboard as CSV.
func Copy(res db.Result) error {
	var buf strings.Builder
	if err := writeCSV(&buf, res); err != nil {
		return err
	}
	return copyText(buf.String())
}

func copyText(text string) error {
	for _, argv := range clipboardCommands {
		path, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, argv[1:]...) //nolint:gosec // argv is a fixed allowlist
		cmd.Stdin = strings.NewReader(text)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return ErrNoClipboard
}
