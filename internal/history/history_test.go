package history

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// statements extracts the statement text of entries, in order, for comparison.
func statements(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Statement)
	}
	return out
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	if want := filepath.Join("/data", "d9s", "history.jsonl"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}

	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	got, err = DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "d9s", "history.jsonl"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestAppendLoad(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		append []Entry
		want   []string // statements, newest first
	}{
		{
			name: "empty store",
			want: nil,
		},
		{
			name:   "single entry round-trip",
			append: []Entry{{Statement: "SELECT 1"}},
			want:   []string{"SELECT 1"},
		},
		{
			name: "newest first",
			append: []Entry{
				{Timestamp: base, Statement: "SELECT 1"},
				{Timestamp: base.Add(time.Minute), Statement: "SELECT 2"},
				{Timestamp: base.Add(2 * time.Minute), Statement: "SELECT 3"},
			},
			want: []string{"SELECT 3", "SELECT 2", "SELECT 1"},
		},
		{
			name: "duplicates collapse to latest occurrence",
			append: []Entry{
				{Timestamp: base, Statement: "SELECT 1"},
				{Timestamp: base.Add(time.Minute), Statement: "SELECT 2"},
				{Timestamp: base.Add(2 * time.Minute), Statement: "SELECT 1"},
			},
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "failed statements are kept",
			append: []Entry{
				{Timestamp: base, Statement: "SELECT boom", OK: false},
				{Timestamp: base.Add(time.Minute), Statement: "SELECT 1", OK: true},
			},
			want: []string{"SELECT 1", "SELECT boom"},
		},
		{
			name: "multi-line statement survives",
			append: []Entry{
				{Timestamp: base, Statement: "SELECT *\nFROM users\nWHERE id = 1"},
			},
			want: []string{"SELECT *\nFROM users\nWHERE id = 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(filepath.Join(t.TempDir(), "sub", "history.jsonl"))
			for _, e := range tt.append {
				if err := s.Append(e); err != nil {
					t.Fatalf("Append(%q) error = %v", e.Statement, err)
				}
			}
			got, err := s.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(statements(got), tt.want) {
				t.Errorf("Load() = %#v, want %#v", statements(got), tt.want)
			}
		})
	}
}

func TestAppendPreservesFields(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "history.jsonl"))
	want := Entry{
		Timestamp:  time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Connection: "prod-pg",
		Database:   "app",
		Statement:  "SELECT 1",
		OK:         true,
		Duration:   1500 * time.Millisecond,
	}
	if err := s.Append(want); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Load() returned %d entries, want 1", len(got))
	}
	if !got[0].Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got[0].Timestamp, want.Timestamp)
	}
	got[0].Timestamp = want.Timestamp // compare the rest by value
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("Load()[0] = %#v, want %#v", got[0], want)
	}
}

func TestAppendIsAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := New(path)
	for _, stmt := range []string{"SELECT 1", "SELECT 2"} {
		if err := s.Append(Entry{Statement: stmt}); err != nil {
			t.Fatalf("Append(%q) error = %v", stmt, err)
		}
	}
	// A second store on the same path must extend the file, not replace it.
	if err := New(path).Append(Entry{Statement: "SELECT 3"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); n != 3 {
		t.Errorf("history file has %d lines, want 3", n)
	}
}

func TestLoadTolerance(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "truncated trailing line",
			raw:  `{"ts":"2026-03-01T12:00:00Z","statement":"SELECT 1"}` + "\n" + `{"ts":"2026-03-01T12:01:00Z","stat`,
			want: []string{"SELECT 1"},
		},
		{
			name: "garbage line in the middle",
			raw: `{"statement":"SELECT 1"}` + "\n" +
				"not json at all\n" +
				`{"statement":"SELECT 2"}` + "\n",
			want: []string{"SELECT 2", "SELECT 1"},
		},
		{
			name: "blank lines ignored",
			raw:  "\n" + `{"statement":"SELECT 1"}` + "\n\n   \n",
			want: []string{"SELECT 1"},
		},
		{
			name: "entry without a statement ignored",
			raw:  `{"connection":"prod-pg"}` + "\n" + `{"statement":"SELECT 1"}` + "\n",
			want: []string{"SELECT 1"},
		},
		{
			name: "only corrupt lines",
			raw:  "{{{\n]]]\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.jsonl")
			if err := os.WriteFile(path, []byte(tt.raw), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			got, err := New(path).Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(statements(got), tt.want) {
				t.Errorf("Load() = %#v, want %#v", statements(got), tt.want)
			}
		})
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	got, err := New(filepath.Join(t.TempDir(), "nope", "history.jsonl")).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load() = %#v, want no entries", got)
	}
}

func TestAppendConcurrent(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "history.jsonl"))
	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.Append(Entry{Statement: strings.Repeat("x", i+1)})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Append(#%d) error = %v", i, err)
		}
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != n {
		t.Errorf("Load() returned %d entries, want %d", len(got), n)
	}
}

func TestFilter(t *testing.T) {
	entries := []Entry{
		{Statement: "SELECT * FROM users"},
		{Statement: "select count(*) from Orders"},
		{Statement: "DELETE FROM user_sessions WHERE id = 1"},
		{Statement: "SHOW TABLES"},
	}
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "empty query keeps everything",
			query: "",
			want: []string{
				"SELECT * FROM users",
				"select count(*) from Orders",
				"DELETE FROM user_sessions WHERE id = 1",
				"SHOW TABLES",
			},
		},
		{
			name:  "case-insensitive substring",
			query: "user",
			want: []string{
				"SELECT * FROM users",
				"DELETE FROM user_sessions WHERE id = 1",
			},
		},
		{
			name:  "uppercase query matches lowercase statement",
			query: "ORDERS",
			want:  []string{"select count(*) from Orders"},
		},
		{
			name:  "matches anywhere in the statement",
			query: "count(*)",
			want:  []string{"select count(*) from Orders"},
		},
		{
			name:  "no match",
			query: "vacuum",
			want:  nil,
		},
		{
			name:  "order is preserved",
			query: "from",
			want: []string{
				"SELECT * FROM users",
				"select count(*) from Orders",
				"DELETE FROM user_sessions WHERE id = 1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filter(entries, tt.query)
			if !reflect.DeepEqual(statements(got), tt.want) {
				t.Errorf("Filter(entries, %q) = %#v, want %#v", tt.query, statements(got), tt.want)
			}
		})
	}
}
