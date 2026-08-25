// Package meta parses and answers psql-style backslash commands. The commands
// are answered from the driver's catalog rather than sent to the engine, so
// `\dt` works the same on every engine that can list tables.
package meta

import (
	"fmt"
	"strings"
)

// Command is one parsed backslash command.
type Command struct {
	// Verb is the command's name without the backslash: "dt", "d", "l", "?".
	Verb string
	// Plus reports the + suffix, as in `\d+ users`.
	Plus bool
	// Arg is the command's argument with any quoting removed, "" when none.
	Arg string
}

// commandInfo describes one supported command for help and suggestions.
type commandInfo struct {
	verb string
	args string
	desc string
}

// commands is every supported command, in the order help lists them.
var commands = []commandInfo{
	{"l", "", "list databases"},
	{"d", "[table]", "describe a table, or list tables; \\d+ adds size, indexes, comments"},
	{"dt", "", "list tables"},
	{"dn", "", "list schemas"},
	{"du", "", "list roles"},
	{"df", "", "list functions"},
	{"?", "", "list these commands"},
	{"q", "", "quit"},
}

// Is reports whether stmt is a meta-command: its first non-blank byte is a
// backslash.
func Is(stmt string) bool {
	return strings.HasPrefix(strings.TrimSpace(stmt), `\`)
}

// Parse reads a backslash command into its verb, + flag, and argument. An
// unknown verb is an error naming the closest supported command, so a typo
// answers with a suggestion instead of reaching the engine.
func Parse(stmt string) (Command, error) {
	s := strings.TrimSpace(stmt)
	if !strings.HasPrefix(s, `\`) {
		return Command{}, fmt.Errorf("%q is not a meta-command", stmt)
	}
	rest := s[1:]
	i := 0
	for i < len(rest) && isVerbByte(rest[i]) {
		i++
	}
	if i == 0 && strings.HasPrefix(rest, "?") {
		i = 1
	}
	verb := rest[:i]
	if verb == "" {
		return Command{}, fmt.Errorf(`incomplete meta-command %q (\? lists commands)`, s)
	}
	c := Command{Verb: verb}
	rest = rest[i:]
	if strings.HasPrefix(rest, "+") {
		c.Plus = true
		rest = rest[1:]
	}
	if !known(c.Verb) {
		return Command{}, unknownVerb(c.Verb)
	}
	if rest != "" && !isSpace(rest[0]) {
		return Command{}, fmt.Errorf(`malformed meta-command %q (\? lists commands)`, s)
	}
	arg, tail, err := readArg(strings.TrimSpace(rest))
	if err != nil {
		return Command{}, fmt.Errorf(`%v in %q`, err, s)
	}
	if strings.TrimSpace(tail) != "" {
		return Command{}, fmt.Errorf(`\%s takes at most one argument, got %q too`, verb, strings.TrimSpace(tail))
	}
	c.Arg = arg
	return c, nil
}

// readArg reads one argument off the front of s: a double-quoted name with ""
// escaping, or a bare word ending at whitespace.
func readArg(s string) (arg, tail string, err error) {
	if s == "" {
		return "", "", nil
	}
	if s[0] != '"' {
		i := 0
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		return s[:i], s[i:], nil
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		if s[i] == '"' {
			if i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i += 2
				continue
			}
			return b.String(), s[i+1:], nil
		}
		b.WriteByte(s[i])
		i++
	}
	return "", "", fmt.Errorf("unterminated quote")
}

// Help returns every supported command with its one-line description, one row
// per command, ready to render as a result table.
func Help() [][]string {
	rows := make([][]string, len(commands))
	for i, c := range commands {
		name := `\` + c.verb
		if c.args != "" {
			name += " " + c.args
		}
		rows[i] = []string{name, c.desc}
	}
	return rows
}

func known(verb string) bool {
	for _, c := range commands {
		if c.verb == verb {
			return true
		}
	}
	return false
}

// unknownVerb names the closest supported command, when one is close enough
// to be a plausible typo.
func unknownVerb(verb string) error {
	best, dist := "", 3 // suggestions further than 2 edits away mislead
	for _, c := range commands {
		if d := editDistance(verb, c.verb); d < dist {
			best, dist = c.verb, d
		}
	}
	if best == "" {
		return fmt.Errorf(`\%s is not a command (\? lists commands)`, verb)
	}
	return fmt.Errorf(`\%s is not a command; did you mean \%s? (\? lists commands)`, verb, best)
}

// editDistance is the Levenshtein distance between two short verbs.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev = cur
	}
	return prev[len(b)]
}

func isVerbByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }
