package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andreim/d9s/internal/db"
)

func sampleResult() db.Result {
	return db.Result{
		Columns: []string{"id", "name", "note"},
		Rows: [][]string{
			{"1", "alice", "NULL"},
			{"2", `he said "hi"`, "comma, inside"},
			{"3", "multi\nline", ""},
		},
	}
}

func TestFormatForPath(t *testing.T) {
	tests := map[string]Format{
		"out.csv":       CSV,
		"out.json":      JSON,
		"OUT.JSON":      JSON,
		"out":           CSV,
		"out.txt":       CSV,
		"dir.json/file": CSV,
	}
	for path, want := range tests {
		t.Run(path, func(t *testing.T) {
			if got := FormatForPath(path); got != want {
				t.Errorf("FormatForPath(%q) = %q, want %q", path, got, want)
			}
		})
	}
}

func TestWriteCSVQuotesSpecialCharacters(t *testing.T) {
	var buf strings.Builder
	if err := Write(&buf, sampleResult(), CSV); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()

	want := "id,name,note\n" +
		"1,alice,NULL\n" +
		"2,\"he said \"\"hi\"\"\",\"comma, inside\"\n" +
		"3,\"multi\nline\",\n"
	if got != want {
		t.Errorf("CSV =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteCSVEmptyResult(t *testing.T) {
	var buf strings.Builder
	if err := Write(&buf, db.Result{}, CSV); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("CSV of an empty result = %q, want empty output", got)
	}
}

func TestWriteJSONMapsNullsAndKeysByColumn(t *testing.T) {
	var buf strings.Builder
	if err := Write(&buf, sampleResult(), JSON); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var records []map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &records); err != nil {
		t.Fatalf("decoding JSON export: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}
	if records[0]["name"] != "alice" {
		t.Errorf("records[0][name] = %v, want alice", records[0]["name"])
	}
	if note, ok := records[0]["note"]; !ok || note != nil {
		t.Errorf("records[0][note] = %v (present=%v), want an explicit null", note, ok)
	}
	if records[2]["note"] != "" {
		t.Errorf("records[2][note] = %v, want an empty string (not null)", records[2]["note"])
	}
}

func TestWriteJSONEmptyResultIsEmptyArray(t *testing.T) {
	var buf strings.Builder
	if err := Write(&buf, db.Result{Columns: []string{"a"}}, JSON); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("JSON of a row-less result = %q, want []", got)
	}
}

func TestWriteJSONIgnoresExtraColumns(t *testing.T) {
	res := db.Result{Columns: []string{"a", "b"}, Rows: [][]string{{"1"}}}
	var buf strings.Builder
	if err := Write(&buf, res, JSON); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var records []map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &records); err != nil {
		t.Fatalf("decoding JSON export: %v", err)
	}
	if _, ok := records[0]["b"]; ok {
		t.Errorf("record = %v, want no key for the column the row lacks", records[0])
	}
}

func TestWriteFile(t *testing.T) {
	for _, name := range []string{"out.csv", "out.json"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			rows, err := WriteFile(path, sampleResult())
			if err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if rows != 3 {
				t.Errorf("wrote %d rows, want 3", rows)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if len(data) == 0 {
				t.Error("exported file is empty")
			}
		})
	}
}

func TestWriteFileReportsUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "out.csv")
	if _, err := WriteFile(path, sampleResult()); err == nil {
		t.Fatal("WriteFile succeeded, want an error for an unwritable path")
	}
}
