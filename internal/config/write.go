package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrExists is returned when adding a connection whose name is already in the
// file, or renaming one onto a name that is taken.
var ErrExists = errors.New("connection already exists")

// ErrNotFound is returned when the connection to update or remove is not in
// the file.
var ErrNotFound = errors.New("connection not found")

// defaultIndent is the nesting width used for a file that has none to copy,
// matching Sample.
const defaultIndent = 2

// File is a configuration file open for editing. It keeps the file as text and
// rewrites only the lines of the connection being changed, so comments, key
// order, blank lines, and the alignment of everything else survive untouched.
//
// Every mutation re-parses and re-validates the result before accepting it, so
// a File can never hold a document that Load would reject.
type File struct {
	path   string
	lines  []string
	indent int
}

// Open reads path for editing. A missing file is not an error: it yields an
// empty File that the first Add fills in and Write creates.
func Open(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{path: path, indent: defaultIndent}, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	if _, _, err := parse(raw, path); err != nil {
		return nil, err
	}
	f := &File{path: path, lines: splitLines(raw)}
	f.indent = detectIndent(raw)
	return f, nil
}

// Path returns the file the editor reads and writes.
func (f *File) Path() string { return f.path }

// Bytes returns the file as it currently stands, including unsaved edits.
func (f *File) Bytes() []byte { return []byte(joinLines(f.lines)) }

// Connections returns the connections in the file, validated exactly as Load
// validates them, so ports and other defaults are filled in.
func (f *File) Connections() ([]Connection, []Warning, error) {
	cfg, warns, err := parse(f.Bytes(), f.path)
	if err != nil {
		return nil, nil, err
	}
	return cfg.Connections, warns, nil
}

// Add appends conn to the file. It returns an error wrapping ErrExists when
// the name is already taken.
func (f *File) Add(conn Connection) error {
	if err := normalize(&conn); err != nil {
		return err
	}
	existing, _, err := f.Connections()
	if err != nil {
		return err
	}
	if indexByName(existing, conn.Name) >= 0 {
		return fmt.Errorf("adding connection %q: %w", conn.Name, ErrExists)
	}

	node, err := buildConnection(&conn)
	if err != nil {
		return err
	}
	block, err := f.renderItem(node)
	if err != nil {
		return err
	}
	lines, err := f.insertItem(block)
	if err != nil {
		return err
	}
	return f.commit(lines)
}

// Update replaces the connection named name with conn, which may rename it.
// Only the keys whose values actually change are rewritten, so comments inside
// the entry survive alongside those around it. It returns an error wrapping
// ErrNotFound when name is not in the file, or ErrExists when the new name
// belongs to a different connection.
func (f *File) Update(name string, conn Connection) error {
	if err := normalize(&conn); err != nil {
		return err
	}
	existing, _, err := f.Connections()
	if err != nil {
		return err
	}
	i := indexByName(existing, name)
	if i < 0 {
		return fmt.Errorf("updating connection %q: %w", name, ErrNotFound)
	}
	if j := indexByName(existing, conn.Name); j >= 0 && j != i {
		return fmt.Errorf("renaming connection %q to %q: %w", name, conn.Name, ErrExists)
	}

	item, err := f.itemNode(i)
	if err != nil {
		return err
	}
	old := existing[i]
	merge(item, connectionFields(), &old, &conn)

	// The head and foot comments sit outside the lines being replaced and stay
	// in the file as they are; re-emitting them here would duplicate them.
	item.HeadComment, item.FootComment = "", ""

	block, err := f.renderItem(item)
	if err != nil {
		return err
	}
	start, end, err := f.itemSpan(i)
	if err != nil {
		return err
	}
	return f.commit(splice(f.lines, start, end, block))
}

// Remove deletes the connection named name, together with the comment block
// introducing it. It returns an error wrapping ErrNotFound when there is
// nothing to remove.
func (f *File) Remove(name string) error {
	existing, _, err := f.Connections()
	if err != nil {
		return err
	}
	i := indexByName(existing, name)
	if i < 0 {
		return fmt.Errorf("removing connection %q: %w", name, ErrNotFound)
	}

	start, end, err := f.itemSpan(i)
	if err != nil {
		return err
	}
	start = f.headStart(start)
	lines := splice(f.lines, start, end, nil)
	return f.commit(closeGap(lines, start))
}

// Write saves the file atomically: the bytes go to a temporary file in the
// same directory, which is then renamed over the original, so a failure at any
// point leaves the previous file intact.
func (f *File) Write() error {
	body := f.Bytes()
	if _, _, err := parse(body, f.path); err != nil {
		return err
	}

	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	// A no-op once the rename has happened; on every failure path it is what
	// keeps a partial file from being left behind.
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
	if err := os.Rename(name, f.path); err != nil {
		return fmt.Errorf("replacing %s: %w", f.path, err)
	}
	return nil
}

// AddConnection adds conn to the config file at path and saves it.
func AddConnection(path string, conn Connection) error {
	return edit(path, func(f *File) error { return f.Add(conn) })
}

// UpdateConnection replaces the connection named name in the config file at
// path with conn, which may rename it, and saves the file.
func UpdateConnection(path, name string, conn Connection) error {
	return edit(path, func(f *File) error { return f.Update(name, conn) })
}

// RemoveConnection deletes the connection named name from the config file at
// path and saves it.
func RemoveConnection(path, name string) error {
	return edit(path, func(f *File) error { return f.Remove(name) })
}

// edit opens path, applies one change, and saves.
func edit(path string, change func(*File) error) error {
	f, err := Open(path)
	if err != nil {
		return err
	}
	if err := change(f); err != nil {
		return err
	}
	return f.Write()
}

// commit accepts lines as the new contents once they parse and validate, so a
// rejected edit leaves the File exactly as it was.
func (f *File) commit(lines []string) error {
	if _, _, err := parse([]byte(joinLines(lines)), f.path); err != nil {
		return err
	}
	f.lines = lines
	return nil
}

// normalize validates one connection on its own and fills in the defaults Load
// would fill in, so a value is only written when it truly differs from what is
// already there. Cross-connection rules such as duplicate names are checked
// when the edited document is re-parsed.
func normalize(conn *Connection) error {
	cfg := Config{Connections: []Connection{*conn}}
	if _, err := cfg.validate(); err != nil {
		return err
	}
	*conn = cfg.Connections[0]
	return nil
}

// indexByName returns the position of the connection named name, or -1.
func indexByName(conns []Connection, name string) int {
	for i := range conns {
		if conns[i].Name == name {
			return i
		}
	}
	return -1
}

// document parses the current lines into a node tree.
func (f *File) document() (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(f.Bytes(), &doc); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", f.path, err)
	}
	return &doc, nil
}

// sequence returns the node holding the connections list, or nil when the file
// has no such key yet.
func (f *File) sequence() (*yaml.Node, error) {
	doc, err := f.document()
	if err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}
	value, _ := mapEntry(root, "connections")
	if value == nil || value.Kind != yaml.SequenceNode {
		return nil, nil
	}
	return value, nil
}

// itemNode returns the mapping node of the i-th connection.
func (f *File) itemNode(i int) (*yaml.Node, error) {
	seq, err := f.sequence()
	if err != nil {
		return nil, err
	}
	if seq == nil || i >= len(seq.Content) {
		return nil, fmt.Errorf("config %s: connection #%d is not in the file", f.path, i+1)
	}
	return seq.Content[i], nil
}

// itemSpan returns the first and last line (1-based, inclusive) that belong to
// the i-th connection, excluding the comment block above it and any blank
// lines after it.
func (f *File) itemSpan(i int) (start, end int, err error) {
	item, err := f.itemNode(i)
	if err != nil {
		return 0, 0, err
	}
	start = item.Line
	if start < 1 || start > len(f.lines) {
		return 0, 0, fmt.Errorf("config %s: connection #%d is on line %d, outside the file", f.path, i+1, start)
	}
	dash := indentOf(f.lines[start-1])
	end = start
	for n := start; n < len(f.lines); n++ {
		line := f.lines[n]
		if strings.TrimSpace(line) == "" {
			continue // may be interior; only a deeper line after it counts
		}
		if indentOf(line) <= dash {
			break // the next item, or a dedent out of the list
		}
		end = n + 1
	}
	return start, end, nil
}

// headStart walks back from an item's first line over the comment lines that
// introduce it, so removing the item removes its comment too.
func (f *File) headStart(start int) int {
	for start > 1 {
		prev := strings.TrimSpace(f.lines[start-2])
		if !strings.HasPrefix(prev, "#") {
			break
		}
		start--
	}
	return start
}

// renderItem encodes one connection mapping as the lines of a list item,
// indented to match the file.
func (f *File) renderItem(item *yaml.Node) ([]string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(f.indent)
	if err := enc.Encode(item); err != nil {
		return nil, fmt.Errorf("encoding connection: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding connection: %w", err)
	}

	body := splitLines(buf.Bytes())
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("encoding connection: produced nothing")
	}

	pad := strings.Repeat(" ", f.itemIndent())
	out := make([]string, 0, len(body))
	for i, line := range body {
		switch {
		case i == 0:
			out = append(out, pad+"- "+line)
		case line == "":
			out = append(out, "")
		default:
			out = append(out, pad+"  "+line)
		}
	}
	return out, nil
}

// itemIndent returns the number of spaces before the "-" of a list item,
// copied from the existing items so an added one lines up with them.
func (f *File) itemIndent() int {
	seq, err := f.sequence()
	if err == nil && seq != nil && len(seq.Content) > 0 {
		if col := seq.Content[0].Column; col >= 3 {
			return col - 3
		}
		return 0
	}
	return f.indent
}

// insertItem places an encoded item at the end of the connections list,
// creating the list, and the file, when there is none.
func (f *File) insertItem(block []string) ([]string, error) {
	seq, err := f.sequence()
	if err != nil {
		return nil, err
	}
	if seq != nil && len(seq.Content) > 0 {
		_, end, err := f.itemSpan(len(seq.Content) - 1)
		if err != nil {
			return nil, err
		}
		return splice(f.lines, end+1, end, block), nil
	}

	// No list to append to. Put the key back with the item under it, keeping
	// whatever else the file holds.
	if key, ok := f.keyLine("connections"); ok {
		return splice(f.lines, key, key, append([]string{"connections:"}, block...)), nil
	}
	lines := trimTrailingBlanks(f.lines)
	return append(append(lines, "connections:"), block...), nil
}

// keyLine returns the 1-based line of a top-level key.
func (f *File) keyLine(key string) (int, bool) {
	doc, err := f.document()
	if err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return 0, false
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i].Line, true
		}
	}
	return 0, false
}

// splice replaces lines start..end (1-based, inclusive) with block. An end
// below start inserts at start without removing anything.
func splice(lines []string, start, end int, block []string) []string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, 0, len(lines)-(end-start+1)+len(block))
	out = append(out, lines[:start-1]...)
	out = append(out, block...)
	if end >= start {
		return append(out, lines[end:]...)
	}
	return append(out, lines[start-1:]...)
}

// closeGap collapses the blank lines left where a removed item used to be, so
// deleting an entry does not leave a double blank behind.
func closeGap(lines []string, at int) []string {
	blank := func(i int) bool { return i >= 1 && i <= len(lines) && strings.TrimSpace(lines[i-1]) == "" }
	if blank(at-1) && (at > len(lines) || blank(at)) {
		return splice(lines, at-1, at-1, nil)
	}
	return lines
}

// trimTrailingBlanks drops the blank lines at the end of a file.
func trimTrailingBlanks(lines []string) []string {
	out := append([]string(nil), lines...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// splitLines splits a file into lines, dropping the newline that terminates
// the last one so joinLines can put exactly one back.
func splitLines(raw []byte) []string {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// joinLines renders lines as a file ending in exactly one newline.
func joinLines(lines []string) string {
	lines = trimTrailingBlanks(lines)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// indentOf returns the number of leading spaces on a line.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// detectIndent reads the nesting width out of the file, so an edit indents new
// nested blocks the way the rest of the file does.
func detectIndent(raw []byte) int {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 {
		return defaultIndent
	}
	if n := nestedIndent(doc.Content[0]); n > 0 {
		return n
	}
	return defaultIndent
}

// nestedIndent returns the column difference between the first mapping key
// that has a nested block and that block's own first entry.
func nestedIndent(n *yaml.Node) int {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if value.Kind == yaml.MappingNode && len(value.Content) > 0 && value.Line > key.Line {
				if d := value.Column - key.Column; d > 0 {
					return d
				}
			}
		}
	}
	for _, c := range n.Content {
		if d := nestedIndent(c); d > 0 {
			return d
		}
	}
	return 0
}

// mapEntry returns the value node stored under key in a mapping, and the index
// of the key within Content, or nil and -1.
func mapEntry(m *yaml.Node, key string) (*yaml.Node, int) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

// setEntry stores value under key, keeping the comments already attached to
// the entry, and appending the key when the mapping does not have it yet.
func setEntry(m *yaml.Node, key string, value *yaml.Node) {
	current, _ := mapEntry(m, key)
	if current == nil {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			value)
		return
	}
	head, line, foot := current.HeadComment, current.LineComment, current.FootComment
	*current = *value
	current.HeadComment, current.LineComment, current.FootComment = head, line, foot
}

// removeEntry drops key and its value from a mapping.
func removeEntry(m *yaml.Node, key string) {
	if _, i := mapEntry(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

// nodeEqual compares two nodes by the value they encode, ignoring comments and
// the style they were written in.
func nodeEqual(a, b *yaml.Node) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	case a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value:
		return false
	case len(a.Content) != len(b.Content):
		return false
	}
	for i := range a.Content {
		if !nodeEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// field describes how one struct field is written as a mapping entry.
type field[T any] struct {
	key string
	// build returns the node for the field's value, or nil when the field is
	// empty and the key does not belong in the file at all.
	build func(v *T) *yaml.Node
	// mergeInto updates a nested block in place rather than replacing it, so
	// comments inside the block survive. Only called when both sides have one.
	mergeInto func(node *yaml.Node, old, cur *T)
}

// merge rewrites the mapping so it describes cur instead of old, touching only
// the entries whose value actually changes. A nil old means every field is new.
func merge[T any](m *yaml.Node, fields []field[T], old, cur *T) {
	for _, f := range fields {
		want := f.build(cur)
		if old != nil && nodeEqual(f.build(old), want) {
			continue // unchanged: leave this entry exactly as the user wrote it
		}
		current, _ := mapEntry(m, f.key)
		switch {
		case want == nil:
			removeEntry(m, f.key)
		case f.mergeInto != nil && old != nil && current != nil:
			f.mergeInto(current, old, cur)
		default:
			setEntry(m, f.key, want)
		}
	}
}

// buildConnection renders a whole connection as a fresh mapping node.
func buildConnection(conn *Connection) (*yaml.Node, error) {
	m := &yaml.Node{Kind: yaml.MappingNode}
	merge(m, connectionFields(), nil, conn)
	if len(m.Content) == 0 {
		return nil, fmt.Errorf("connection %q has nothing to write", conn.Name)
	}
	return m, nil
}

// str returns a string entry, or nil when the value is empty.
func str(v string) *yaml.Node {
	if v == "" {
		return nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// num returns an integer entry, or nil when the value is zero.
func num(v int) *yaml.Node {
	if v == 0 {
		return nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
}

// yes returns a boolean entry, or nil when the value is false, since false is
// what an absent key already means.
func yes(v bool) *yaml.Node {
	if !v {
		return nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
}

// list returns a sequence of strings, or nil when there are none.
func list(values []string) *yaml.Node {
	if len(values) == 0 {
		return nil
	}
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range values {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	return n
}

// block renders a nested mapping from its own field list, or nil when there is
// no block to write.
func block[T any](v *T, fields []field[T]) *yaml.Node {
	if v == nil {
		return nil
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	merge(m, fields, nil, v)
	if len(m.Content) == 0 {
		return nil
	}
	return m
}

// connectionFields lists the keys of a connection in the order a freshly
// written entry spells them out.
func connectionFields() []field[Connection] {
	return []field[Connection]{
		{key: "name", build: func(c *Connection) *yaml.Node { return str(c.Name) }},
		{key: "type", build: func(c *Connection) *yaml.Node { return str(string(c.Type)) }},
		{key: "host", build: func(c *Connection) *yaml.Node { return str(c.Host) }},
		{key: "port", build: func(c *Connection) *yaml.Node { return num(c.Port) }},
		{key: "user", build: func(c *Connection) *yaml.Node { return str(c.User) }},
		{key: "password", build: func(c *Connection) *yaml.Node { return str(c.Password) }},
		{key: "database", build: func(c *Connection) *yaml.Node { return str(c.Database) }},
		{
			key:   "ssh",
			build: func(c *Connection) *yaml.Node { return block(c.SSH, sshFields()) },
			mergeInto: func(n *yaml.Node, old, cur *Connection) {
				merge(n, sshFields(), old.SSH, cur.SSH)
			},
		},
		{
			key:   "tls",
			build: func(c *Connection) *yaml.Node { return block(c.TLS, tlsFields()) },
			mergeInto: func(n *yaml.Node, old, cur *Connection) {
				merge(n, tlsFields(), old.TLS, cur.TLS)
			},
		},
		{key: "connect_timeout", build: func(c *Connection) *yaml.Node {
			if c.ConnectTimeout == 0 {
				return nil
			}
			// A duration marshals as a bare nanosecond count, which no one
			// wants to read in their own config file.
			return str(c.ConnectTimeout.String())
		}},
		{key: "protocol", build: func(c *Connection) *yaml.Node { return str(string(c.Protocol)) }},
		{key: "mode", build: func(c *Connection) *yaml.Node { return str(string(c.Mode)) }},
		{key: "master_name", build: func(c *Connection) *yaml.Node { return str(c.MasterName) }},
		{key: "addresses", build: func(c *Connection) *yaml.Node { return list(c.Addresses) }},
		{key: "allow_write", build: func(c *Connection) *yaml.Node { return yes(c.AllowWrite) }},
	}
}

// sshFields lists the keys of an ssh block.
func sshFields() []field[SSH] {
	return []field[SSH]{
		{key: "bastion", build: func(s *SSH) *yaml.Node { return str(s.Bastion) }},
		{key: "user", build: func(s *SSH) *yaml.Node { return str(s.User) }},
		{key: "port", build: func(s *SSH) *yaml.Node { return num(s.Port) }},
		{key: "agent_socket", build: func(s *SSH) *yaml.Node { return str(s.AgentSocket) }},
	}
}

// tlsFields lists the keys of a tls block.
func tlsFields() []field[TLS] {
	return []field[TLS]{
		{key: "mode", build: func(t *TLS) *yaml.Node { return str(string(t.Mode)) }},
		{key: "ca", build: func(t *TLS) *yaml.Node { return str(t.CA) }},
		{key: "cert", build: func(t *TLS) *yaml.Node { return str(t.Cert) }},
		{key: "key", build: func(t *TLS) *yaml.Node { return str(t.Key) }},
		{key: "server_name", build: func(t *TLS) *yaml.Node { return str(t.ServerName) }},
	}
}
