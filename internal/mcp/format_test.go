package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutBytes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		limit   int
		wantCut bool
		want    string
	}{
		{name: "short values pass through", in: "hello", limit: 10, want: "hello"},
		{name: "a value at the limit is untouched", in: "hello", limit: 5, want: "hello"},
		{name: "an oversized value is marked", in: "hello world, again", limit: 14, wantCut: true, want: "hello " + cutMarker},
		{name: "a limit below the marker cuts without one", in: "hello", limit: 2, wantCut: true, want: "he"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cut := cutBytes(tt.in, tt.limit)
			if cut != tt.wantCut {
				t.Errorf("cutBytes(%q, %d) reported cut=%v, want %v", tt.in, tt.limit, cut, tt.wantCut)
			}
			if got != tt.want {
				t.Errorf("cutBytes(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
			if len(got) > tt.limit {
				t.Errorf("cutBytes(%q, %d) returned %d bytes, over the limit", tt.in, tt.limit, len(got))
			}
			if !utf8.ValidString(got) {
				t.Errorf("cutBytes(%q, %d) = %q, which is not valid UTF-8", tt.in, tt.limit, got)
			}
		})
	}
}

func TestCutBytesNeverSplitsARune(t *testing.T) {
	// Every cut of a multi-byte string must land on a rune boundary, or the
	// response carries a replacement character where the data was.
	in := strings.Repeat("héllo→", 50)
	for limit := 1; limit <= len(in); limit++ {
		got, _ := cutBytes(in, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("cutBytes at limit %d produced invalid UTF-8: %q", limit, got)
		}
	}
}

func TestGrid(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		rows    [][]string
		want    []string
		absent  []string
	}{
		{
			name:    "an empty result says so",
			columns: []string{"id"},
			want:    []string{"(no rows)"},
		},
		{
			name:    "a small result carries no notice",
			columns: []string{"id", "name"},
			rows:    [][]string{{"1", "ada"}, {"2", "grace"}},
			want:    []string{"id", "name", "ada", "grace"},
			absent:  []string{"Truncated"},
		},
		{
			name:    "columns are aligned to their widest value",
			columns: []string{"id", "name"},
			rows:    [][]string{{"1", "ada"}, {"1000", "grace"}},
			want:    []string{"1    | ada", "1000 | grace"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := grid(tt.columns, tt.rows)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("grid output is missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("grid output should not contain %q:\n%s", absent, got)
				}
			}
		})
	}
}

func TestGridStaysUnderTheByteCap(t *testing.T) {
	// Rows well under the row cap can still blow the byte cap between them.
	rows := make([][]string, MaxRows)
	for i := range rows {
		rows[i] = []string{strings.Repeat("y", maxCellBytes-1)}
	}
	got := grid([]string{"payload"}, rows)
	if len(got) > MaxBytes {
		t.Errorf("grid returned %d bytes, over the %d-byte cap", len(got), MaxBytes)
	}
	if !strings.Contains(got, "byte response cap") {
		t.Errorf("the byte cap fired without saying so:\n%s", tail(got))
	}
	if !strings.Contains(got, "of 200 rows shown") {
		t.Errorf("the notice does not say how many rows came back:\n%s", tail(got))
	}
}

func TestRedactorScrubsRegisteredSecrets(t *testing.T) {
	red := &redactor{}
	red.add("swordfish")
	red.add("xy") // too short to scrub safely
	red.add("")

	got := red.scrub("auth failed for app using swordfish and xy")
	if strings.Contains(got, "swordfish") {
		t.Errorf("the secret survived scrubbing: %q", got)
	}
	if !strings.Contains(got, redactedMarker) {
		t.Errorf("the scrubbed text does not mark the removal: %q", got)
	}
	if !strings.Contains(got, "xy") {
		t.Errorf("a value below the length floor should be left alone: %q", got)
	}
}
