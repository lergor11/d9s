package db

import (
	"strings"

	"github.com/andreim/d9s/internal/config"
)

// Split breaks a script into executable statements. SQL engines split on ';'
// outside strings/comments; Redis splits one command per line.
func Split(engine config.EngineType, script string) []string {
	if engine == config.Redis {
		return splitRedis(script)
	}
	return splitSQL(script, engine == config.ClickHouse)
}

// Destructive returns the subset of stmts considered destructive for the
// engine (DROP/TRUNCATE/ALTER, DELETE/UPDATE without WHERE, FLUSHALL, ...).
func Destructive(engine config.EngineType, stmts []string) []string {
	var out []string
	for _, s := range stmts {
		if isDestructive(engine, s) {
			out = append(out, s)
		}
	}
	return out
}

func splitRedis(script string) []string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Lexical element kinds produced by sqlConsume.
const (
	lexRune    = iota // one plain byte of code
	lexComment        // -- line comment or /* block comment */
	lexString         // '...' or $tag$...$tag$
	lexIdent          // "..." or `...` (clickhouse)
)

// TokenKind classifies one lexical element produced by Tokenize.
type TokenKind int

const (
	// TokenWord is an unquoted name or keyword.
	TokenWord TokenKind = iota
	// TokenQuoted is a quoted name: "..." or, on ClickHouse, `...`.
	TokenQuoted
	// TokenString is a string literal, dollar-quoted ones included, or a
	// quoted Redis argument.
	TokenString
	// TokenNumber is a numeric literal, its fraction and exponent included.
	TokenNumber
	// TokenComment is a comment, from its opener to its end.
	TokenComment
	// TokenCommand is the command name at the start of a Redis line.
	TokenCommand
	// TokenPunct is one byte of punctuation: a comma, a paren, an operator.
	TokenPunct
)

// Token is one lexical element of a script: what it is, where it sits, and the
// text it holds with the delimiters of a quoted name removed.
type Token struct {
	Kind  TokenKind
	Start int // byte offset of the first byte
	End   int // byte offset just past the last byte
	Text  string
}

// Tokenize splits a script into its lexical elements, dropping whitespace but
// keeping comments, which callers that only look at code can skip by kind. A
// string literal, a quoted name and a comment each stay one element, so a
// keyword or a semicolon written inside one is never reported as code. Redis
// scripts are lexed one command per line: the first argument of a line is a
// TokenCommand and the rest are arguments.
func Tokenize(engine config.EngineType, script string) []Token {
	if engine == config.Redis {
		return tokenizeRedis(script)
	}
	return tokenizeSQL(script, engine == config.ClickHouse)
}

// IsNameByte reports whether c can appear inside an unquoted SQL name. It is
// the character class Tokenize builds a TokenWord out of.
func IsNameByte(c byte) bool { return isWordByte(c) }

func tokenizeSQL(s string, backtick bool) []Token {
	var toks []Token
	for i := 0; i < len(s); {
		end, kind := sqlConsume(s, i, backtick)
		switch kind {
		case lexComment:
			toks = append(toks, Token{Kind: TokenComment, Start: i, End: end, Text: s[i:end]})
		case lexString:
			toks = append(toks, Token{Kind: TokenString, Start: i, End: end, Text: s[i:end]})
		case lexIdent:
			toks = append(toks, Token{Kind: TokenQuoted, Start: i, End: end, Text: unquoteName(s[i:end])})
		default:
			switch c := s[i]; {
			case isSpaceByte(c):
			case isWordByte(c):
				j := i
				for j < len(s) && isWordByte(s[j]) {
					j++
				}
				tok := Token{Kind: TokenWord, Start: i, End: j}
				if isDigitByte(c) {
					tok.Kind, tok.End = TokenNumber, numberEnd(s, j)
				}
				tok.Text = s[tok.Start:tok.End]
				toks = append(toks, tok)
				i = tok.End
				continue
			default:
				toks = append(toks, Token{Kind: TokenPunct, Start: i, End: end, Text: s[i:end]})
			}
		}
		i = end
	}
	return toks
}

func isDigitByte(c byte) bool { return '0' <= c && c <= '9' }

// numberEnd extends a numeric literal whose leading digits end at j over a
// fractional part and a signed exponent, so 1.5 and 2e-9 each stay one token.
func numberEnd(s string, j int) int {
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && isWordByte(s[j]) {
			j++
		}
	}
	if j > 0 && j < len(s) && (s[j-1] == 'e' || s[j-1] == 'E') && (s[j] == '+' || s[j] == '-') {
		j++
		for j < len(s) && isWordByte(s[j]) {
			j++
		}
	}
	return j
}

// tokenizeRedis lexes a Redis script the way the command splitter reads it: one
// command per line, arguments separated by whitespace, quoted arguments kept
// whole, and a '#' line taken as a comment.
func tokenizeRedis(script string) []Token {
	var toks []Token
	for i, first := 0, true; i < len(script); {
		switch c := script[i]; {
		case c == '\n':
			first = true
			i++
		case isSpaceByte(c):
			i++
		case c == '#' && first:
			end := len(script)
			if j := strings.IndexByte(script[i:], '\n'); j >= 0 {
				end = i + j
			}
			toks = append(toks, Token{Kind: TokenComment, Start: i, End: end, Text: script[i:end]})
			i = end
		default:
			end := redisArgEnd(script, i)
			tok := Token{Kind: TokenWord, Start: i, End: end, Text: script[i:end]}
			switch {
			case first:
				tok.Kind = TokenCommand
			case c == '"' || c == '\'':
				tok.Kind = TokenString
			case isDigitByte(c):
				tok.Kind = TokenNumber
			}
			toks = append(toks, tok)
			first = false
			i = end
		}
	}
	return toks
}

// redisArgEnd returns the index just past the argument starting at i: a quoted
// argument runs to its closing quote, honoring the backslash escapes the
// command splitter accepts, and a bare one runs to the next whitespace.
func redisArgEnd(s string, i int) int {
	quote := s[i]
	if quote != '"' && quote != '\'' {
		for i < len(s) && !isSpaceByte(s[i]) {
			i++
		}
		return i
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		case '\n':
			return j
		}
	}
	return len(s)
}

// unquoteName strips the delimiters of a quoted name and collapses the doubled
// quotes inside it.
func unquoteName(s string) string {
	if len(s) < 2 {
		return s
	}
	quote := s[:1]
	s = strings.TrimSuffix(strings.TrimPrefix(s, quote), quote)
	return strings.ReplaceAll(s, quote+quote, quote)
}

// sqlConsume consumes one lexical element of s starting at i and returns the
// index just past it plus its kind. Unterminated elements run to end of input.
func sqlConsume(s string, i int, backtick bool) (int, int) {
	switch c := s[i]; {
	case c == '-' && i+1 < len(s) && s[i+1] == '-':
		if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
			return i + j + 1, lexComment
		}
		return len(s), lexComment
	case c == '/' && i+1 < len(s) && s[i+1] == '*':
		if j := strings.Index(s[i+2:], "*/"); j >= 0 {
			return i + 2 + j + 2, lexComment
		}
		return len(s), lexComment
	case c == '\'':
		j := i + 1
		for j < len(s) {
			switch s[j] {
			case '\\':
				j += 2
			case '\'':
				if j+1 < len(s) && s[j+1] == '\'' {
					j += 2 // '' escape
					continue
				}
				return j + 1, lexString
			default:
				j++
			}
		}
		return len(s), lexString
	case c == '"':
		j := i + 1
		for j < len(s) {
			if s[j] == '"' {
				if j+1 < len(s) && s[j+1] == '"' {
					j += 2 // "" escape
					continue
				}
				return j + 1, lexIdent
			}
			j++
		}
		return len(s), lexIdent
	case c == '`' && backtick:
		if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
			return i + 1 + j + 1, lexIdent
		}
		return len(s), lexIdent
	case c == '$':
		if end, ok := dollarQuoteEnd(s, i); ok {
			return end, lexString
		}
	}
	return i + 1, lexRune
}

// dollarQuoteEnd matches a Postgres dollar-quoted string ($$...$$ or
// $tag$...$tag$) starting at i and returns the index past its closing delimiter.
func dollarQuoteEnd(s string, i int) (int, bool) {
	j := i + 1
	for j < len(s) && isDollarTagByte(s[j]) {
		j++
	}
	if j >= len(s) || s[j] != '$' {
		return 0, false
	}
	delim := s[i : j+1]
	if k := strings.Index(s[j+1:], delim); k >= 0 {
		return j + 1 + k + len(delim), true
	}
	return len(s), true
}

func isDollarTagByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

func isWordByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c >= 0x80
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// splitSQL splits on ';' outside strings, quoted identifiers, and comments.
// Statements holding no code (empty or comment-only) are dropped.
func splitSQL(script string, backtick bool) []string {
	var out []string
	start, hasCode := 0, false
	flush := func(end int) {
		if stmt := strings.TrimSpace(script[start:end]); stmt != "" && hasCode {
			out = append(out, stmt)
		}
	}
	for _, t := range tokenizeSQL(script, backtick) {
		switch {
		case t.Kind == TokenPunct && t.Text == ";":
			flush(t.Start)
			start, hasCode = t.End, false
		case t.Kind != TokenComment:
			// A statement made of nothing but comments has nothing to run.
			hasCode = true
		}
	}
	flush(len(script))
	return out
}

// topLevelSQLWords returns the uppercased word tokens of stmt that sit outside
// strings, quoted identifiers, comments, and parentheses (so a WHERE inside a
// subquery or a string literal is not reported).
func topLevelSQLWords(stmt string, backtick bool) []string {
	var words []string
	depth := 0
	for _, t := range tokenizeSQL(stmt, backtick) {
		switch {
		case t.Kind == TokenPunct && t.Text == "(":
			depth++
		case t.Kind == TokenPunct && t.Text == ")":
			if depth > 0 {
				depth--
			}
		case t.Kind == TokenWord && depth == 0:
			words = append(words, strings.ToUpper(t.Text))
		}
	}
	return words
}

func isDestructive(engine config.EngineType, stmt string) bool {
	if engine == config.Redis {
		fields := strings.Fields(stmt)
		if len(fields) == 0 {
			return false
		}
		switch strings.ToUpper(fields[0]) {
		case "FLUSHALL", "FLUSHDB", "DEL", "SHUTDOWN":
			return true
		case "CONFIG":
			return len(fields) > 1 && strings.EqualFold(fields[1], "SET")
		}
		return false
	}
	words := topLevelSQLWords(stmt, engine == config.ClickHouse)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "DROP", "TRUNCATE", "ALTER":
		return true
	case "DELETE", "UPDATE":
		for _, w := range words[1:] {
			if w == "WHERE" {
				return false
			}
		}
		return true
	}
	return false
}
