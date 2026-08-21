// Package snippets stores named queries in a YAML file the user can edit by
// hand or check into a dotfiles repository. An entry is either global or tied
// to a single connection. Nothing is cached between calls, so an edit made
// outside d9s is visible on the next Load.
package snippets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrExists is returned by Save when an entry with the same name and scope is
// already stored and the caller did not ask to overwrite it.
var ErrExists = errors.New("saved query already exists")

// ErrNotFound is returned by Delete when no entry has the given name and scope.
var ErrNotFound = errors.New("saved query not found")

// Entry is one saved query. An empty Connection means the entry is global and
// available from every connection.
type Entry struct {
	Name       string `yaml:"name"`
	Connection string `yaml:"connection,omitempty"`
	Query      string `yaml:"query"`
}

// file is the on-disk shape of the queries file.
type file struct {
	Queries []Entry `yaml:"queries"`
}

// Scope says whether an entry applies to every connection or to just one.
type Scope int

const (
	// ScopeGlobal marks an entry that is available from any connection.
	ScopeGlobal Scope = iota
	// ScopeConnection marks an entry tied to one connection.
	ScopeConnection
)

// String returns "global" or "connection".
func (s Scope) String() string {
	if s == ScopeConnection {
		return "connection"
	}
	return "global"
}

// Scoped pairs an entry with how it applies to the connection it was selected
// for, so a browser can mark each row without re-deriving it.
type Scoped struct {
	Entry
	Scope Scope
}

// Store is a YAML file of saved queries. Its methods are safe for concurrent
// use within a process and re-read the file on every call.
type Store struct {
	mu   sync.Mutex
	path string
}

// New returns a Store backed by the file at path. The file and its parent
// directories are created by the first Save.
func New(path string) *Store { return &Store{path: path} }

// Default returns a Store at DefaultPath.
func Default() (*Store, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return New(p), nil
}

// DefaultPath returns $XDG_CONFIG_HOME/d9s/queries.yaml, falling back to
// ~/.config/d9s/queries.yaml when XDG_CONFIG_HOME is unset. D9S_QUERIES
// overrides both.
func DefaultPath() (string, error) {
	if p := os.Getenv("D9S_QUERIES"); p != "" {
		return p, nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "d9s", "queries.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".config", "d9s", "queries.yaml"), nil
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// Load returns the saved entries in the order they appear in the file. A
// missing file yields no entries and no error, so a first run is not a
// failure. A file that does not parse is an error rather than an empty list,
// because a hand edit that broke the syntax must not look like data loss.
// Entries without a name cannot be addressed and are skipped.
func (s *Store) Load() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// load reads the file. The caller must hold s.mu.
func (s *Store) load() ([]Entry, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading saved queries %s: %w", s.path, err)
	}
	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parsing saved queries %s: %w", s.path, err)
	}
	out := make([]Entry, 0, len(f.Queries))
	for _, e := range f.Queries {
		e.Name = strings.TrimSpace(e.Name)
		e.Connection = strings.TrimSpace(e.Connection)
		if e.Name == "" {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// Save stores e, replacing any entry with the same name and scope. Names are
// compared case-insensitively and the connection scope exactly, so "Daily" and
// "daily" are the same entry but the same name under two connections is not.
// When an entry would be replaced and overwrite is false, Save changes nothing
// and returns an error wrapping ErrExists, leaving the decision to the caller.
func (s *Store) Save(e Entry, overwrite bool) error {
	e.Name = strings.TrimSpace(e.Name)
	e.Connection = strings.TrimSpace(e.Connection)
	if e.Name == "" {
		return fmt.Errorf("saving to %s: a saved query needs a name", s.path)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.load()
	if err != nil {
		return err
	}
	if i := indexOf(entries, e.Name, e.Connection); i >= 0 {
		if !overwrite {
			return fmt.Errorf("saving %q: %w", e.Name, ErrExists)
		}
		entries[i] = e
	} else {
		entries = append(entries, e)
	}
	return s.write(entries)
}

// Delete removes the entry with the given name and scope, matching names
// case-insensitively as Save does. It returns an error wrapping ErrNotFound
// when there is nothing to remove.
func (s *Store) Delete(name, connection string) error {
	name = strings.TrimSpace(name)
	connection = strings.TrimSpace(connection)

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.load()
	if err != nil {
		return err
	}
	i := indexOf(entries, name, connection)
	if i < 0 {
		return fmt.Errorf("deleting %q: %w", name, ErrNotFound)
	}
	return s.write(slices.Delete(entries, i, i+1))
}

// write replaces the file with entries, via a temporary file in the same
// directory so a failed or interrupted write leaves the previous contents
// intact. The caller must hold s.mu.
func (s *Store) write(entries []Entry) error {
	body, err := yaml.Marshal(file{Queries: entries})
	if err != nil {
		return fmt.Errorf("encoding saved queries: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating saved queries directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".queries-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	// After a successful rename there is nothing at name and this is a no-op;
	// on every failure path it is what keeps a partial file from being left
	// behind.
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting permissions on %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flushing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("replacing %s: %w", s.path, err)
	}
	return nil
}

// indexOf returns the position of the entry with the given name and scope, or
// -1. Names match case-insensitively, connections exactly.
func indexOf(entries []Entry, name, connection string) int {
	for i, e := range entries {
		if e.Connection == connection && strings.EqualFold(e.Name, name) {
			return i
		}
	}
	return -1
}

// ForConnection returns the entries visible from conn — the global ones plus
// those scoped to conn — each marked with its scope. Entries belonging to
// another connection are left out, and file order is preserved so the user
// controls the ordering by editing the file. An empty conn yields only the
// global entries.
func ForConnection(entries []Entry, conn string) []Scoped {
	var out []Scoped
	for _, e := range entries {
		switch {
		case e.Connection == "":
			out = append(out, Scoped{Entry: e, Scope: ScopeGlobal})
		case conn != "" && e.Connection == conn:
			out = append(out, Scoped{Entry: e, Scope: ScopeConnection})
		}
	}
	return out
}

// FilterByName returns the entries whose name contains query, matched
// case-insensitively. A blank query matches every entry.
func FilterByName(entries []Scoped, query string) []Scoped {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entries
	}
	var out []Scoped
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), q) {
			out = append(out, e)
		}
	}
	return out
}

// MarshalYAML writes the entry with its query as a literal block scalar when
// the text allows it, so a person editing the file sees real line breaks
// rather than escapes.
//
// The style has to be chosen here rather than left to the encoder: given a
// multi-line string whose first line starts with a space or a tab, yaml.v3
// emits a block scalar that it cannot read back. Anything that cannot be
// represented as a block scalar byte for byte — leading whitespace on the
// first line, trailing whitespace on any line, carriage returns — falls back
// to a quoted scalar, which is uglier but always faithful.
func (e Entry) MarshalYAML() (any, error) {
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value *yaml.Node) {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
	}

	add("name", &yaml.Node{Kind: yaml.ScalarNode, Value: e.Name})
	if e.Connection != "" {
		add("connection", &yaml.Node{Kind: yaml.ScalarNode, Value: e.Connection})
	}

	query := &yaml.Node{Kind: yaml.ScalarNode, Value: e.Query}
	switch {
	case literalSafe(e.Query):
		query.Style = yaml.LiteralStyle
	case strings.Contains(e.Query, "\n"):
		query.Style = yaml.DoubleQuotedStyle
	}
	add("query", query)

	return m, nil
}

// literalSafe reports whether s survives a round trip through a YAML literal
// block scalar. Single-line text is left to the encoder, which quotes it when
// it has to.
func literalSafe(s string) bool {
	if !strings.Contains(s, "\n") {
		return false
	}
	lines := strings.Split(s, "\n")
	// A block scalar takes its indentation from the first line, and yaml.v3
	// mis-emits the explicit indicator it would need if that line is blank or
	// itself indented.
	if lines[0] == "" || lines[0][0] == ' ' || lines[0][0] == '\t' {
		return false
	}
	for _, line := range lines {
		// Trailing whitespace cannot be expressed in a block scalar, and a
		// carriage return would come back as a bare newline.
		if strings.TrimRight(line, " \t") != line {
			return false
		}
		if strings.ContainsRune(line, '\r') {
			return false
		}
	}
	return true
}
