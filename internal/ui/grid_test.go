package ui

import (
	"reflect"
	"testing"
)

// col pulls one column out of sorted rows, so a test compares the ordering it
// cares about instead of whole rows. A cell past the row's width reads as
// blank, the same way cellAt treats it.
func col(rows [][]string, i int) []string {
	out := make([]string, len(rows))
	for j, row := range rows {
		out[j] = cellAt(row, i)
	}
	return out
}

func TestSortRows(t *testing.T) {
	tests := []struct {
		name  string
		rows  [][]string
		col   int
		desc  bool
		check int      // the column to read the expectation from
		want  []string // that column's cells, in the sorted order
	}{
		{
			name: "text sorts case-insensitively",
			rows: [][]string{{"pear"}, {"Apple"}, {"banana"}},
			col:  0,
			want: []string{"Apple", "banana", "pear"},
		},
		{
			name: "numbers sort numerically, not as text",
			rows: [][]string{{"10"}, {"2"}, {"9"}},
			col:  0,
			want: []string{"2", "9", "10"},
		},
		{
			name: "descending reverses the order",
			rows: [][]string{{"10"}, {"2"}, {"9"}},
			col:  0,
			desc: true,
			want: []string{"10", "9", "2"},
		},
		{
			name: "NULL and blank sort after real numbers",
			rows: [][]string{{nullCell}, {"3"}, {""}, {"1"}},
			col:  0,
			// The unparsable cells fall back to a text comparison between
			// themselves, which puts the blank before the NULL.
			want: []string{"1", "3", "", nullCell},
		},
		{
			name: "one non-number makes the whole column text",
			rows: [][]string{{"10"}, {"2"}, {"x"}},
			col:  0,
			want: []string{"10", "2", "x"},
		},
		{
			name:  "a column of NULLs alone is not numeric",
			rows:  [][]string{{"b", nullCell}, {"a", nullCell}},
			col:   1,
			check: 1,
			want:  []string{nullCell, nullCell},
		},
		{
			name: "sorting by a column past the row width leaves the order",
			rows: [][]string{{"b"}, {"a"}},
			col:  3,
			want: []string{"b", "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := make([][]string, len(tt.rows))
			copy(before, tt.rows)
			got := sortRows(tt.rows, tt.col, tt.desc)
			check := tt.check
			if check == 0 && tt.col < len(tt.rows[0]) {
				check = tt.col
			}
			if gotCol := col(got, check); !reflect.DeepEqual(gotCol, tt.want) {
				t.Errorf("sortRows() column %d = %q, want %q", check, gotCol, tt.want)
			}
			if !reflect.DeepEqual(tt.rows, before) {
				t.Error("sortRows modified the underlying result")
			}
		})
	}
}

func TestSortRowsIsStable(t *testing.T) {
	rows := [][]string{{"a", "1"}, {"b", "1"}, {"c", "0"}, {"d", "1"}}
	got := sortRows(rows, 1, false)
	if want := []string{"c", "a", "b", "d"}; !reflect.DeepEqual(col(got, 0), want) {
		t.Errorf("equal keys reordered: got %q, want %q", col(got, 0), want)
	}
}

func TestFilterRows(t *testing.T) {
	rows := [][]string{
		{"1", "connection reset", "ERROR"},
		{"2", "listening", "info"},
		{"3", "Errand done", "info"},
	}

	tests := []struct {
		name   string
		filter string
		col    int // -1 means every column
		want   []string
	}{
		{name: "blank keeps everything", filter: "", col: -1, want: []string{"1", "2", "3"}},
		{name: "spaces alone keep everything", filter: "  ", col: -1, want: []string{"1", "2", "3"}},
		{name: "matches any column, case-insensitively", filter: "err", col: -1, want: []string{"1", "3"}},
		{name: "a selected column narrows the match", filter: "err", col: 2, want: []string{"1"}},
		{name: "the needle's case does not matter either", filter: "ERR", col: 1, want: []string{"3"}},
		{name: "no match leaves nothing", filter: "warn", col: -1, want: []string{}},
		{name: "a column past the row width matches nothing", filter: "err", col: 5, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterRows(rows, tt.filter, tt.col)
			if gotCol := col(got, 0); !reflect.DeepEqual(gotCol, tt.want) {
				t.Errorf("filterRows(%q, col %d) kept %q, want %q", tt.filter, tt.col, gotCol, tt.want)
			}
		})
	}
}

func TestPrettyJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // empty means "not JSON"
	}{
		{
			name: "an object is indented",
			raw:  `{"a":1,"b":[2,3]}`,
			want: "{\n  \"a\": 1,\n  \"b\": [\n    2,\n    3\n  ]\n}",
		},
		{
			name: "an array is indented",
			raw:  `[1,2]`,
			want: "[\n  1,\n  2\n]",
		},
		{
			name: "surrounding whitespace is ignored",
			raw:  "  {\"a\":1}\n",
			want: "{\n  \"a\": 1\n}",
		},
		{name: "plain text is left alone", raw: "connection reset"},
		{name: "a bare number is not a document", raw: "42"},
		{name: "a quoted string is not a document", raw: `"hi"`},
		{name: "broken JSON is not mangled", raw: `{"a":`},
		{name: "too short to be JSON", raw: "{"},
		{name: "empty is not JSON", raw: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := prettyJSON(tt.raw)
			if wantOK := tt.want != ""; ok != wantOK {
				t.Fatalf("prettyJSON(%q) ok = %v, want %v", tt.raw, ok, wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("prettyJSON(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
