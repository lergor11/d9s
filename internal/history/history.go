// Package history records executed statements in an append-only JSONL file so
// the query view can offer shell-like history search and reuse. Only the
// statement text as typed is stored; resolved secrets never reach the file.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxLineBytes bounds a single history line, so a corrupt file cannot make the
// reader allocate without limit.
const maxLineBytes = 4 << 20

// Entry is one executed statement as recorded in the history file.
type Entry struct {
	Timestamp  time.Time     `json:"ts"`
	Connection string        `json:"connection"`
	Database   string        `json:"database"`
	Statement  string        `json:"statement"`
	OK         bool          `json:"ok"`
	Duration   time.Duration `json:"duration_ns"`
}

// Store is an append-only JSONL history file. Its methods are safe for
// concurrent use within a process.
type Store struct {
	mu   sync.Mutex
	path string
}

// New returns a Store backed by the file at path. The file and its parent
// directories are created by the first Append.
func New(path string) *Store { return &Store{path: path} }

// Default returns a Store at DefaultPath.
func Default() (*Store, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return New(p), nil
}

// DefaultPath returns $XDG_DATA_HOME/d9s/history.jsonl, falling back to
// ~/.local/share/d9s/history.jsonl when XDG_DATA_HOME is unset.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "d9s", "history.jsonl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "d9s", "history.jsonl"), nil
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// Append writes e as one JSON line at the end of the history file, creating
// the file and its parent directories when needed.
func (s *Store) Append(e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding history entry: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating history directory %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening history file %s: %w", s.path, err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing history file %s: %w", s.path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing history file %s: %w", s.path, err)
	}
	return nil
}

// Load returns the recorded entries most recent first, de-duplicated by
// statement text with the latest occurrence winning. A missing file yields no
// entries and no error. Lines that are not a valid entry — a partially written
// last line, say — are skipped rather than failing the whole load.
func (s *Store) Load() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening history file %s: %w", s.path, err)
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, maxLineBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Statement == "" {
			continue
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// An over-long line means the rest of the file is unusable;
			// keep what was read instead of losing the whole history.
			return dedup(entries), nil
		}
		return nil, fmt.Errorf("reading history file %s: %w", s.path, err)
	}
	return dedup(entries), nil
}

// dedup reverses entries into newest-first order, keeping only the latest
// occurrence of each statement.
func dedup(entries []Entry) []Entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]Entry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if _, dup := seen[e.Statement]; dup {
			continue
		}
		seen[e.Statement] = struct{}{}
		out = append(out, e)
	}
	return out
}

// Filter returns the entries whose statement contains query, matched
// case-insensitively. An empty query matches every entry.
func Filter(entries []Entry, query string) []Entry {
	if query == "" {
		return entries
	}
	q := strings.ToLower(query)
	var out []Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Statement), q) {
			out = append(out, e)
		}
	}
	return out
}
