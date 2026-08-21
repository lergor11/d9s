package snippets

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// newStore returns a Store over a fresh temporary file that does not exist yet.
func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "queries.yaml"))
}

// names extracts the names of scoped entries, in order, for comparison.
func names(entries []Scoped) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestDefaultPath(t *testing.T) {
	tests := []struct {
		name      string
		override  string
		xdgConfig string
		want      string
		fromHome  string
	}{
		{
			name:     "D9S_QUERIES wins",
			override: "/tmp/custom.yaml",
			want:     "/tmp/custom.yaml",
		},
		{
			name:      "D9S_QUERIES wins over XDG",
			override:  "/tmp/custom.yaml",
			xdgConfig: "/conf",
			want:      "/tmp/custom.yaml",
		},
		{
			name:      "XDG_CONFIG_HOME",
			xdgConfig: "/conf",
			want:      filepath.Join("/conf", "d9s", "queries.yaml"),
		},
		{
			name:     "home fallback",
			fromHome: filepath.Join(".config", "d9s", "queries.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("D9S_QUERIES", tt.override)
			t.Setenv("XDG_CONFIG_HOME", tt.xdgConfig)

			want := tt.want
			if tt.fromHome != "" {
				home, err := os.UserHomeDir()
				if err != nil {
					t.Skipf("no home directory: %v", err)
				}
				want = filepath.Join(home, tt.fromHome)
			}

			got, err := DefaultPath()
			if err != nil {
				t.Fatalf("DefaultPath() error = %v", err)
			}
			if got != want {
				t.Errorf("DefaultPath() = %q, want %q", got, want)
			}
		})
	}
}

// TestSaveLoadRoundTrip covers the query shapes most likely to be mangled by
// the YAML encoder: multi-line text, trailing semicolons and whitespace, and
// indentation on the first line.
func TestSaveLoadRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"single line", "SELECT 1"},
		{"single line trailing semicolon", "SELECT 1;"},
		{"trailing newline", "SELECT 1;\n"},
		{"multi line", "SELECT id, email\nFROM users\nWHERE created_at > now() - interval '1 day';\n"},
		{"multi line no trailing newline", "SELECT id\nFROM users;"},
		{"blank line inside", "SELECT 1;\n\nSELECT 2;\n"},
		{"indented continuation", "SELECT id\n  FROM users\n  WHERE id = 1;\n"},
		{"indented first line", "  SELECT id\n  FROM users;\n"},
		{"tab indented first line", "\tSELECT id\nFROM users;\n"},
		{"trailing spaces on a line", "SELECT id,   \n       email\nFROM users;\n"},
		{"trailing blank lines", "SELECT 1;\n\n\n"},
		{"carriage returns", "SELECT id\r\nFROM users;\r\n"},
		{"only whitespace line inside", "SELECT 1;\n   \nSELECT 2;\n"},
		{"unicode and quotes", "SELECT 'héllo — \"world\"' AS greeting;\n"},
		{"leading blank line", "\nSELECT 1;\n"},
		{"empty query", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			want := Entry{Name: tt.name, Connection: "prod-pg", Query: tt.query}
			if err := s.Save(want, false); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			got, err := s.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("Load() returned %d entries, want 1", len(got))
			}
			if got[0] != want {
				t.Errorf("round trip changed the entry:\n got %#v\nwant %#v", got[0], want)
			}
		})
	}
}

// TestSaveUsesBlockScalar checks the readable half of the storage promise:
// ordinary multi-line SQL is written as a literal block, not as an escaped
// one-liner, so the file stays hand-editable.
func TestSaveUsesBlockScalar(t *testing.T) {
	s := newStore(t)
	query := "SELECT id, email\nFROM users\nORDER BY id;\n"
	if err := s.Save(Entry{Name: "signups", Query: query}, false); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "query: |") {
		t.Errorf("query was not written as a literal block scalar:\n%s", body)
	}
	if strings.Contains(body, `\n`) {
		t.Errorf("query was written with escaped newlines:\n%s", body)
	}
}

func TestSaveOverwriteDetection(t *testing.T) {
	const original = "SELECT 1;\n"
	const replacement = "SELECT 2;\n"

	tests := []struct {
		name       string
		second     Entry
		overwrite  bool
		wantErr    error
		wantCount  int
		wantQuery  string // query stored under the original name
		wantSecond string // query stored under second.Name when it is a new entry
	}{
		{
			name:      "same name and scope is refused",
			second:    Entry{Name: "daily", Connection: "prod-pg", Query: replacement},
			wantErr:   ErrExists,
			wantCount: 1,
			wantQuery: original,
		},
		{
			name:      "same name different case is refused",
			second:    Entry{Name: "DAILY", Connection: "prod-pg", Query: replacement},
			wantErr:   ErrExists,
			wantCount: 1,
			wantQuery: original,
		},
		{
			name:      "overwrite replaces in place",
			second:    Entry{Name: "daily", Connection: "prod-pg", Query: replacement},
			overwrite: true,
			wantCount: 1,
			wantQuery: replacement,
		},
		{
			name:       "same name other connection is a new entry",
			second:     Entry{Name: "daily", Connection: "analytics-ch", Query: replacement},
			wantCount:  2,
			wantQuery:  original,
			wantSecond: replacement,
		},
		{
			name:       "same name global scope is a new entry",
			second:     Entry{Name: "daily", Query: replacement},
			wantCount:  2,
			wantQuery:  original,
			wantSecond: replacement,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			first := Entry{Name: "daily", Connection: "prod-pg", Query: original}
			if err := s.Save(first, false); err != nil {
				t.Fatalf("Save(first) error = %v", err)
			}

			err := s.Save(tt.second, tt.overwrite)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Save() error = %v, want one wrapping %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			got, err := s.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("Load() returned %d entries, want %d: %#v", len(got), tt.wantCount, got)
			}
			if i := indexOf(got, "daily", "prod-pg"); got[i].Query != tt.wantQuery {
				t.Errorf("prod-pg entry query = %q, want %q", got[i].Query, tt.wantQuery)
			}
			if tt.wantSecond != "" {
				i := indexOf(got, tt.second.Name, tt.second.Connection)
				if i < 0 {
					t.Fatalf("second entry not stored: %#v", got)
				}
				if got[i].Query != tt.wantSecond {
					t.Errorf("second entry query = %q, want %q", got[i].Query, tt.wantSecond)
				}
			}
		})
	}
}

func TestDelete(t *testing.T) {
	stored := []Entry{
		{Name: "daily", Connection: "prod-pg", Query: "SELECT 1;"},
		{Name: "daily", Query: "SELECT 2;"},
		{Name: "weekly", Connection: "analytics-ch", Query: "SELECT 3;"},
	}
	tests := []struct {
		name       string
		delName    string
		delConn    string
		wantErr    error
		wantRemain []string // "name@connection" of the survivors, in order
	}{
		{
			name:       "scoped entry",
			delName:    "daily",
			delConn:    "prod-pg",
			wantRemain: []string{"daily@", "weekly@analytics-ch"},
		},
		{
			name:       "global entry with the same name",
			delName:    "daily",
			wantRemain: []string{"daily@prod-pg", "weekly@analytics-ch"},
		},
		{
			name:       "name matched case-insensitively",
			delName:    "WEEKLY",
			delConn:    "analytics-ch",
			wantRemain: []string{"daily@prod-pg", "daily@"},
		},
		{
			name:       "unknown name",
			delName:    "nope",
			wantErr:    ErrNotFound,
			wantRemain: []string{"daily@prod-pg", "daily@", "weekly@analytics-ch"},
		},
		{
			name:       "right name wrong scope",
			delName:    "weekly",
			delConn:    "prod-pg",
			wantErr:    ErrNotFound,
			wantRemain: []string{"daily@prod-pg", "daily@", "weekly@analytics-ch"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			for _, e := range stored {
				if err := s.Save(e, false); err != nil {
					t.Fatalf("Save(%q) error = %v", e.Name, err)
				}
			}

			err := s.Delete(tt.delName, tt.delConn)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Delete() error = %v, want one wrapping %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			got, err := s.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			var remain []string
			for _, e := range got {
				remain = append(remain, e.Name+"@"+e.Connection)
			}
			if !reflect.DeepEqual(remain, tt.wantRemain) {
				t.Errorf("remaining = %v, want %v", remain, tt.wantRemain)
			}
		})
	}
}

func TestForConnection(t *testing.T) {
	entries := []Entry{
		{Name: "global one", Query: "SELECT 1;"},
		{Name: "pg only", Connection: "prod-pg", Query: "SELECT 2;"},
		{Name: "ch only", Connection: "analytics-ch", Query: "SELECT 3;"},
		{Name: "global two", Query: "SELECT 4;"},
	}
	tests := []struct {
		name       string
		conn       string
		wantNames  []string
		wantScopes []Scope
	}{
		{
			name:       "connection sees its own plus global",
			conn:       "prod-pg",
			wantNames:  []string{"global one", "pg only", "global two"},
			wantScopes: []Scope{ScopeGlobal, ScopeConnection, ScopeGlobal},
		},
		{
			name:       "other connection",
			conn:       "analytics-ch",
			wantNames:  []string{"global one", "ch only", "global two"},
			wantScopes: []Scope{ScopeGlobal, ScopeConnection, ScopeGlobal},
		},
		{
			name:       "unknown connection sees only global",
			conn:       "redis-cache",
			wantNames:  []string{"global one", "global two"},
			wantScopes: []Scope{ScopeGlobal, ScopeGlobal},
		},
		{
			name:       "no connection sees only global",
			conn:       "",
			wantNames:  []string{"global one", "global two"},
			wantScopes: []Scope{ScopeGlobal, ScopeGlobal},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ForConnection(entries, tt.conn)
			if !reflect.DeepEqual(names(got), tt.wantNames) {
				t.Errorf("names = %v, want %v", names(got), tt.wantNames)
			}
			var scopes []Scope
			for _, e := range got {
				scopes = append(scopes, e.Scope)
			}
			if !reflect.DeepEqual(scopes, tt.wantScopes) {
				t.Errorf("scopes = %v, want %v", scopes, tt.wantScopes)
			}
		})
	}
}

func TestScopeString(t *testing.T) {
	if got := ScopeGlobal.String(); got != "global" {
		t.Errorf("ScopeGlobal.String() = %q, want %q", got, "global")
	}
	if got := ScopeConnection.String(); got != "connection" {
		t.Errorf("ScopeConnection.String() = %q, want %q", got, "connection")
	}
}

func TestFilterByName(t *testing.T) {
	entries := ForConnection([]Entry{
		{Name: "Daily signups"},
		{Name: "daily revenue"},
		{Name: "weekly churn"},
		{Name: "SLOW QUERIES"},
	}, "")

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty matches all", "", []string{"Daily signups", "daily revenue", "weekly churn", "SLOW QUERIES"}},
		{"blank matches all", "   ", []string{"Daily signups", "daily revenue", "weekly churn", "SLOW QUERIES"}},
		{"case-insensitive substring", "daily", []string{"Daily signups", "daily revenue"}},
		{"uppercase query", "DAILY", []string{"Daily signups", "daily revenue"}},
		{"matches uppercase names", "slow", []string{"SLOW QUERIES"}},
		{"mid-word substring", " churn", []string{"weekly churn"}},
		{"no match", "nothing", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := names(FilterByName(entries, tt.query))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterByName(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantNames []string
		wantErr   bool
	}{
		{
			name:      "missing file is empty and not an error",
			wantNames: nil,
		},
		{
			name:      "empty file",
			body:      "",
			wantNames: nil,
		},
		{
			name:      "hand written entries",
			body:      "queries:\n  - name: one\n    query: SELECT 1;\n  - name: two\n    connection: prod-pg\n    query: SELECT 2;\n",
			wantNames: []string{"one", "two"},
		},
		{
			name:      "entry without a name is skipped",
			body:      "queries:\n  - query: SELECT 1;\n  - name: keeper\n    query: SELECT 2;\n",
			wantNames: []string{"keeper"},
		},
		{
			name:      "names and connections are trimmed",
			body:      "queries:\n  - name: '  padded  '\n    connection: '  prod-pg  '\n    query: SELECT 1;\n",
			wantNames: []string{"padded"},
		},
		{
			name:    "broken yaml is an error",
			body:    "queries:\n  - name: one\n   query: oops\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queries.yaml")
			if tt.name != "missing file is empty and not an error" {
				if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			}
			got, err := New(path).Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			var gotNames []string
			for _, e := range got {
				gotNames = append(gotNames, e.Name)
			}
			if !reflect.DeepEqual(gotNames, tt.wantNames) {
				t.Errorf("names = %v, want %v", gotNames, tt.wantNames)
			}
		})
	}
}

// TestLoadSeesExternalEdit is the no-cache requirement: a file changed behind
// the store's back is visible on the next Load.
func TestLoadSeesExternalEdit(t *testing.T) {
	s := newStore(t)
	if err := s.Save(Entry{Name: "first", Query: "SELECT 1;"}, false); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if got, err := s.Load(); err != nil || len(got) != 1 {
		t.Fatalf("Load() = %v, %v; want 1 entry", got, err)
	}

	edited := "queries:\n  - name: added by hand\n    query: |\n      SELECT 42;\n  - name: also added\n    connection: prod-pg\n    query: SELECT 43;\n"
	if err := os.WriteFile(s.Path(), []byte(edited), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []Entry{
		{Name: "added by hand", Query: "SELECT 42;\n"},
		{Name: "also added", Connection: "prod-pg", Query: "SELECT 43;"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %#v, want %#v", got, want)
	}
}

// TestSaveIsAtomicOnFailure makes the write fail after the file already has
// contents, and checks that the previous contents survive and no partial file
// is left in the directory.
func TestSaveIsAtomicOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, directory permissions are not enforced")
	}
	dir := t.TempDir()
	s := New(filepath.Join(dir, "queries.yaml"))
	if err := s.Save(Entry{Name: "keeper", Query: "SELECT 1;\n"}, false); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// A directory that cannot be written to is the simplest way to fail the
	// write at the point where a naive implementation would already have
	// truncated the real file.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := s.Save(Entry{Name: "doomed", Query: "SELECT 2;\n"}, false); err == nil {
		t.Fatal("Save() into a read-only directory succeeded, want an error")
	}

	after, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("ReadFile() after failed save error = %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("failed save changed the file:\n got %q\nwant %q", after, before)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	left, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(left) != 1 || left[0] != s.Path() {
		t.Errorf("directory holds %v, want only %s", left, s.Path())
	}
}

// TestSaveLeavesNoTempFile checks the success path does not litter either.
func TestSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "queries.yaml"))
	for _, name := range []string{"one", "two", "three"} {
		if err := s.Save(Entry{Name: name, Query: "SELECT 1;"}, false); err != nil {
			t.Fatalf("Save(%q) error = %v", name, err)
		}
	}
	got, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(got) != 1 || got[0] != s.Path() {
		t.Errorf("directory holds %v, want only %s", got, s.Path())
	}
}

func TestSavePermissions(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "nested", "queries.yaml"))
	if err := s.Save(Entry{Name: "one", Query: "SELECT 1;"}, false); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fi, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %04o, want %04o", got, 0o600)
	}

	di, err := os.Stat(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %04o, want %04o", got, 0o700)
	}
}

func TestSaveRejectsBlankName(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"", "   ", "\t"} {
		if err := s.Save(Entry{Name: name, Query: "SELECT 1;"}, false); err == nil {
			t.Errorf("Save(name=%q) error = nil, want an error", name)
		}
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Errorf("a rejected save created %s", s.Path())
	}
}

// TestConcurrentSaves exercises the mutex the Store documents.
func TestConcurrentSaves(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Save(Entry{Name: name, Query: "SELECT 1;"}, true); err != nil {
				t.Errorf("Save(%q) error = %v", name, err)
			}
		}()
	}
	wg.Wait()

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 8 {
		t.Errorf("Load() returned %d entries, want 8: %#v", len(got), got)
	}
}
