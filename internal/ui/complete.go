package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/db"
	"github.com/lergor11/d9s/internal/schema"
)

const (
	// completionTimeout bounds one catalog query issued for completion.
	completionTimeout = 30 * time.Second
	// completionMaxRows is the tallest the candidate list gets; it scrolls to
	// keep the selection visible.
	completionMaxRows = 8
	// completionMaxItems caps how many candidates one popup holds, so a
	// thousand-column table cannot fill the buffer with useless work.
	completionMaxItems = 200
	// redisPartialMarker is the row the Redis driver appends when a key scan
	// hit its limit; it names no key, so completion drops it.
	redisPartialMarker = "…"
)

// --- what the cursor expects ------------------------------------------------

// completionKind is the sort of name a cursor position expects.
type completionKind int

const (
	completeNone completionKind = iota
	completeKeywords
	completeTables
	completeColumns
	completeRedisCommands
	completeRedisKeys
)

// completionRequest describes one Tab press: the partial name under the cursor
// and where its candidates come from.
type completionRequest struct {
	kind      completionKind
	word      string   // the partial name the insertion replaces
	qualifier string   // the text before the dot, when the name is qualified
	tables    []string // tables to take columns from, already alias-resolved
}

// tableKeywords introduce a table name.
var tableKeywords = map[string]bool{"FROM": true, "JOIN": true, "INTO": true, "UPDATE": true, "TABLE": true}

// columnKeywords introduce a list of columns. BY covers GROUP BY, ORDER BY and
// PARTITION BY, all of which name columns.
var columnKeywords = map[string]bool{
	"SELECT": true, "WHERE": true, "ON": true, "HAVING": true, "SET": true, "BY": true,
}

// sqlKeywords are the words offered when no schema context applies.
var sqlKeywords = []string{
	"ALTER", "AND", "AS", "ASC", "BEGIN", "BETWEEN", "BY", "CASE", "CAST",
	"COMMIT", "CREATE", "CROSS", "DELETE", "DESC", "DISTINCT", "DROP", "ELSE",
	"END", "EXISTS", "EXPLAIN", "FALSE", "FROM", "FULL", "GROUP", "HAVING",
	"IN", "INNER", "INSERT", "INTERSECT", "INTO", "IS", "JOIN", "LEFT", "LIKE",
	"LIMIT", "NOT", "NULL", "OFFSET", "ON", "OR", "ORDER", "OUTER", "RIGHT",
	"ROLLBACK", "SELECT", "SET", "TABLE", "THEN", "TRUE", "TRUNCATE", "UNION",
	"UPDATE", "USING", "VALUES", "WHEN", "WHERE", "WITH",
}

// sqlKeywordSet answers whether a word is reserved, which is how an alias is
// told apart from the clause that follows a table name.
var sqlKeywordSet = func() map[string]bool {
	set := make(map[string]bool, len(sqlKeywords))
	for _, k := range sqlKeywords {
		set[k] = true
	}
	for _, k := range []string{"ALL", "ANY", "ASOF", "FINAL", "GLOBAL", "PREWHERE", "RETURNING", "SAMPLE", "WINDOW"} {
		set[k] = true
	}
	return set
}()

// completionRequestAt works out what Tab should complete when the cursor sits
// at the given byte offset of buf.
func completionRequestAt(engine config.EngineType, buf string, cursor int) completionRequest {
	cursor = min(max(cursor, 0), len(buf))
	if engine == config.Redis {
		return redisRequestAt(buf, cursor)
	}
	qualifier, word := splitWordAtCursor(buf[:cursor])
	req := completionRequest{word: word, qualifier: qualifier}

	// Only the statement holding the cursor names the tables in scope; a name
	// in an earlier statement of the buffer has nothing to do with here, and a
	// comment names nothing at all.
	toks := codeTokens(statementAt(db.Tokenize(engine, buf), cursor))

	// The clause is read from the tokens before the whole name, qualifier
	// included, so `FROM analytics.` still reads as a table position.
	nameStart := cursor - len(word)
	if qualifier != "" {
		nameStart -= len(qualifier) + 1
	}
	req.kind = clauseKind(tokensBefore(toks, nameStart))
	tables, aliases := statementTables(toks)

	if qualifier != "" {
		if req.kind == completeTables {
			// `FROM db.<Tab>`: the qualifier names a database or schema, and
			// the tables cached under it are the candidates.
			return req
		}
		req.kind = completeColumns
		if table, ok := aliases[strings.ToLower(qualifier)]; ok {
			req.tables = []string{table}
		} else {
			req.tables = []string{qualifier}
		}
		return req
	}
	if req.kind == completeColumns {
		req.tables = tables
		if len(tables) == 0 {
			// Nothing to take columns from yet, so the useful offer is the
			// keyword that would come next.
			req.kind = completeKeywords
		}
	}
	return req
}

// tokensBefore keeps the tokens that end at or before the byte offset.
func tokensBefore(toks []db.Token, offset int) []db.Token {
	for i, t := range toks {
		if t.End > offset {
			return toks[:i]
		}
	}
	return toks
}

// isPunct reports whether the token is one particular punctuation byte.
func isPunct(t db.Token, text string) bool {
	return t.Kind == db.TokenPunct && t.Text == text
}

// isName reports whether the token names something, quoted or not.
func isName(t db.Token) bool {
	return t.Kind == db.TokenWord || t.Kind == db.TokenQuoted
}

// isKeyword reports whether the token is a reserved word. A quoted name never
// is, however it is spelled.
func isKeyword(t db.Token) bool {
	return t.Kind == db.TokenWord && sqlKeywordSet[strings.ToUpper(t.Text)]
}

// clauseKind walks back over the tokens before the partial name and reports
// what the governing clause expects.
func clauseKind(toks []db.Token) completionKind {
	depth := 0
	for i := len(toks) - 1; i >= 0; i-- {
		t := toks[i]
		if t.Kind != db.TokenWord {
			switch {
			case isPunct(t, ")"):
				depth++
			case isPunct(t, "("):
				if depth > 0 {
					depth--
					continue
				}
				// An open paren the statement has not closed: a function call
				// or an INSERT column list, both of which name columns.
				if i > 0 && isName(toks[i-1]) && !isKeyword(toks[i-1]) {
					return completeColumns
				}
			}
			continue
		}
		up := strings.ToUpper(t.Text)
		switch {
		case tableKeywords[up]:
			// Only the position right after the keyword, or after a comma
			// continuing its list, names a table; further along the cursor
			// sits on an alias, where a keyword is the better offer.
			if i == len(toks)-1 || isPunct(toks[len(toks)-1], ",") {
				return completeTables
			}
			return completeKeywords
		case columnKeywords[up]:
			return completeColumns
		}
	}
	return completeKeywords
}

// splitWordAtCursor splits the text before the cursor into the qualifier and
// the partial name to complete: `SELECT u.na` yields "u" and "na".
func splitWordAtCursor(text string) (qualifier, word string) {
	start := len(text)
	for start > 0 && db.IsNameByte(text[start-1]) {
		start--
	}
	word = text[start:]
	if start == 0 || text[start-1] != '.' {
		return "", word
	}
	qStart := start - 1
	for qStart > 0 && (db.IsNameByte(text[qStart-1]) || text[qStart-1] == '.') {
		qStart--
	}
	return text[qStart : start-1], word
}

// statementTables returns the tables the statement names, in appearance order,
// together with the aliases declared for them, keyed in lower case.
func statementTables(toks []db.Token) (tables []string, aliases map[string]string) {
	aliases = map[string]string{}
	for i := 0; i < len(toks); i++ {
		if toks[i].Kind != db.TokenWord || !tableKeywords[strings.ToUpper(toks[i].Text)] {
			continue
		}
		j := i + 1
		for j < len(toks) {
			name, next := readQualifiedName(toks, j)
			if name == "" {
				break
			}
			tables = append(tables, name)
			j = next
			if j < len(toks) && toks[j].Kind == db.TokenWord && strings.EqualFold(toks[j].Text, "AS") {
				j++
			}
			if j < len(toks) && isName(toks[j]) && !isKeyword(toks[j]) {
				aliases[strings.ToLower(toks[j].Text)] = name
				j++
			}
			if j < len(toks) && isPunct(toks[j], ",") {
				j++
				continue
			}
			break
		}
		i = j - 1
	}
	return tables, aliases
}

// readQualifiedName reads one possibly dotted name starting at toks[i] and
// returns it with the index just past it. An empty name means toks[i] does not
// start one.
func readQualifiedName(toks []db.Token, i int) (string, int) {
	if i >= len(toks) || !isName(toks[i]) || isKeyword(toks[i]) {
		return "", i
	}
	name := toks[i].Text
	i++
	for i+1 < len(toks) && isPunct(toks[i], ".") && isName(toks[i+1]) {
		name += "." + toks[i+1].Text
		i += 2
	}
	return name, i
}

// --- Redis ------------------------------------------------------------------

// redisCommands are the command names offered at the start of a line.
var redisCommands = []string{
	"APPEND", "AUTH", "BITCOUNT", "COPY", "DBSIZE", "DECR", "DECRBY", "DEL",
	"ECHO", "EXISTS", "EXPIRE", "EXPIREAT", "FLUSHDB", "GET", "GETBIT",
	"GETDEL", "GETEX", "GETRANGE", "GETSET", "HDEL", "HEXISTS", "HGET",
	"HGETALL", "HINCRBY", "HKEYS", "HLEN", "HMGET", "HSET", "HSETNX", "HVALS",
	"INCR", "INCRBY", "INFO", "KEYS", "LINDEX", "LLEN", "LPOP", "LPUSH",
	"LRANGE", "LREM", "LSET", "LTRIM", "MEMORY", "MGET", "MSET", "OBJECT",
	"PERSIST", "PEXPIRE", "PING", "PTTL", "RANDOMKEY", "RENAME", "RPOP",
	"RPUSH", "SADD", "SCAN", "SCARD", "SDIFF", "SET", "SETEX", "SETNX",
	"SETRANGE", "SINTER", "SISMEMBER", "SMEMBERS", "SREM", "SUNION", "TTL",
	"TYPE", "UNLINK", "ZADD", "ZCARD", "ZCOUNT", "ZINCRBY", "ZRANGE",
	"ZRANGEBYSCORE", "ZRANK", "ZREM", "ZREVRANGE", "ZSCORE",
}

// redisKeyCommands take a key as their first argument.
var redisKeyCommands = map[string]bool{
	"APPEND": true, "COPY": true, "DECR": true, "DECRBY": true, "EXPIRE": true,
	"EXPIREAT": true, "GET": true, "GETBIT": true, "GETDEL": true, "GETEX": true,
	"GETRANGE": true, "GETSET": true, "HDEL": true, "HEXISTS": true, "HGET": true,
	"HGETALL": true, "HINCRBY": true, "HKEYS": true, "HLEN": true, "HMGET": true,
	"HSET": true, "HSETNX": true, "HVALS": true, "INCR": true, "INCRBY": true,
	"LINDEX": true, "LLEN": true, "LPOP": true, "LPUSH": true, "LRANGE": true,
	"LREM": true, "LSET": true, "LTRIM": true, "OBJECT": true, "PERSIST": true,
	"PEXPIRE": true, "PTTL": true, "RENAME": true, "RPOP": true, "RPUSH": true,
	"SADD": true, "SCARD": true, "SET": true, "SETEX": true, "SETNX": true,
	"SETRANGE": true, "SISMEMBER": true, "SMEMBERS": true, "SREM": true,
	"TTL": true, "TYPE": true, "ZADD": true, "ZCARD": true, "ZCOUNT": true,
	"ZINCRBY": true, "ZRANGE": true, "ZRANGEBYSCORE": true, "ZRANK": true,
	"ZREM": true, "ZREVRANGE": true, "ZSCORE": true,
}

// redisMultiKeyCommands take a key in every argument position.
var redisMultiKeyCommands = map[string]bool{
	"DEL": true, "EXISTS": true, "MGET": true, "SDIFF": true, "SINTER": true,
	"SUNION": true, "TOUCH": true, "UNLINK": true, "WATCH": true,
}

// redisLineAt returns the part of the command line that precedes the cursor;
// Redis scripts run one command per line.
func redisLineAt(buf string, cursor int) string {
	line := buf[:cursor]
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	return line
}

// redisArgIn returns the argument being typed at the end of a command line. A
// key may hold any character but whitespace, so only whitespace ends it.
func redisArgIn(line string) string {
	if i := strings.LastIndexAny(line, " \t"); i >= 0 {
		return line[i+1:]
	}
	return line
}

// redisRequestAt works out what Tab completes on the Redis command line holding
// the cursor: the command name at the start of the line, or a key for the
// commands that take one.
func redisRequestAt(buf string, cursor int) completionRequest {
	line := redisLineAt(buf, cursor)
	word := redisArgIn(line)
	fields := strings.Fields(line)
	if len(fields) == 0 || (len(fields) == 1 && word != "") {
		return completionRequest{kind: completeRedisCommands, word: word}
	}
	cmd := strings.ToUpper(fields[0])
	arg := len(fields) // 1-based index of the argument being typed
	if word != "" {
		arg--
	}
	if redisMultiKeyCommands[cmd] || (redisKeyCommands[cmd] && arg == 1) {
		return completionRequest{kind: completeRedisKeys, word: word}
	}
	return completionRequest{kind: completeNone, word: word}
}

// --- candidates -------------------------------------------------------------

// schemaFetch names one catalog query a completion still needs: either the
// database's table listing, or the columns of one table. A Redis key scan is a
// column listing of a prefix, which can legitimately be the empty one, so the
// listing is a flag rather than an empty name.
type schemaFetch struct {
	listing bool
	table   string
}

// completionCandidates gathers everything the request could offer, before the
// partial name narrows it. The second result lists the catalog queries still
// missing; when it is non-empty the candidates are incomplete.
func (q *queryModel) completionCandidates(req completionRequest) ([]string, []schemaFetch) {
	switch req.kind {
	case completeKeywords:
		return casedKeywords(req.word), nil
	case completeRedisCommands:
		return redisCommands, nil
	case completeRedisKeys:
		return q.redisKeyCandidates(req.word)
	case completeTables:
		return q.tableCandidates(req.qualifier)
	case completeColumns:
		return q.columnCandidates(req.tables)
	}
	return nil, nil
}

// tableCandidates lists the cached tables. A qualifier keeps only the tables of
// that database or schema, offered without the qualifier the user already
// typed.
func (q *queryModel) tableCandidates(qualifier string) ([]string, []schemaFetch) {
	names, state := q.cache.Tables()
	if state != schema.Ready {
		return nil, []schemaFetch{{listing: true}}
	}
	if qualifier == "" {
		return names, nil
	}
	prefix := qualifier + "."
	var out []string
	for _, n := range names {
		if len(n) > len(prefix) && strings.EqualFold(n[:len(prefix)], prefix) {
			out = append(out, n[len(prefix):])
		}
	}
	return out, nil
}

// columnCandidates lists the columns of the statement's tables, in the order
// the tables appear, dropping duplicates when several tables share a name.
func (q *queryModel) columnCandidates(tables []string) ([]string, []schemaFetch) {
	if _, state := q.cache.Tables(); state != schema.Ready {
		return nil, []schemaFetch{{listing: true}}
	}
	var (
		out   []string
		need  []schemaFetch
		seen  = map[string]bool{}
		known bool
	)
	for _, t := range tables {
		name, ok := q.cache.Resolve(t)
		if !ok {
			continue // a table the catalog does not know; nothing to offer
		}
		known = true
		cols, state := q.cache.Columns(name)
		if state != schema.Ready {
			need = append(need, schemaFetch{table: name})
			continue
		}
		for _, c := range cols {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	if !known {
		return nil, nil
	}
	return out, need
}

// redisKeyCandidates lists the keys under the typed prefix. A wider scan
// already in the cache answers without a new one, as long as it was not cut
// short by the driver's scan limit.
func (q *queryModel) redisKeyCandidates(prefix string) ([]string, []schemaFetch) {
	for i := len(prefix); i >= 0; i-- {
		keys, state := q.cache.Columns(prefix[:i])
		if state != schema.Ready {
			continue
		}
		full, partial := filterRedisKeys(keys, prefix)
		if partial && i < len(prefix) {
			break // the wider scan is incomplete, so scan the typed prefix
		}
		return full, nil
	}
	return nil, []schemaFetch{{table: prefix}}
}

// filterRedisKeys keeps the scanned keys that start with prefix and reports
// whether the scan they came from was truncated.
func filterRedisKeys(keys []string, prefix string) (out []string, partial bool) {
	for _, k := range keys {
		switch {
		case k == redisPartialMarker:
			partial = true
		case strings.HasPrefix(k, prefix):
			out = append(out, k)
		}
	}
	return out, partial
}

// casedKeywords returns the SQL keywords in the case the user is typing in, so
// a lower-case buffer stays lower-case.
func casedKeywords(word string) []string {
	if word == "" || word != strings.ToLower(word) {
		return sqlKeywords
	}
	out := make([]string, len(sqlKeywords))
	for i, k := range sqlKeywords {
		out[i] = strings.ToLower(k)
	}
	return out
}

// rankCandidates narrows names to those matching word and orders them: names
// starting with the word first, then names merely containing it, each group
// alphabetical and free of duplicates.
func rankCandidates(names []string, word string) []string {
	lower := strings.ToLower(word)
	var prefixed, contained []string
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		switch l := strings.ToLower(n); {
		case strings.HasPrefix(l, lower):
			prefixed = append(prefixed, n)
		case lower != "" && strings.Contains(l, lower):
			contained = append(contained, n)
		}
	}
	sort.Strings(prefixed)
	sort.Strings(contained)
	out := prefixed
	out = append(out, contained...)
	if len(out) > completionMaxItems {
		out = out[:completionMaxItems]
	}
	return out
}

// sharedPrefix returns the text the candidates starting with word all begin
// with, when that is more than the user has already typed. It is what a shell
// inserts before it prints the list. Candidates that merely contain the word
// have no say in it, the way a shell's substring-free completion would.
func sharedPrefix(items []string, word string) string {
	lower := strings.ToLower(word)
	shared, matches := "", 0
	for _, item := range items {
		if !strings.HasPrefix(strings.ToLower(item), lower) {
			continue
		}
		matches++
		if matches == 1 {
			shared = item
			continue
		}
		if shared = commonPrefix(shared, item); len(shared) <= len(word) {
			return ""
		}
	}
	if matches < 2 || len(shared) <= len(word) {
		return ""
	}
	return shared
}

// commonPrefix returns the longest prefix a and b share, comparing without
// regard to case but keeping a's spelling.
func commonPrefix(a, b string) string {
	n := min(len(a), len(b))
	i := 0
	for i < n && lowerByte(a[i]) == lowerByte(b[i]) {
		i++
	}
	return a[:i]
}

func lowerByte(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// --- popup state ------------------------------------------------------------

// completionModel is the state of the Tab-completion popup. open = false means
// no popup is showing.
type completionModel struct {
	open bool
	// all is every candidate of the context, so typing can widen the list
	// again after a backspace; items is all narrowed by the partial name.
	all      []string
	items    []string
	sel      int
	listKind completionKind // what the list holds, which decides quoting
	pending  bool           // a Tab press is waiting for the schema cache to fill
	hint     string         // shown instead of the list while loading
}

func (c *completionModel) close() {
	*c = completionModel{}
}

// move shifts the selection by delta, wrapping around the ends the way a
// shell's completion cycle does.
func (c *completionModel) move(delta int) {
	if len(c.items) == 0 {
		return
	}
	c.sel = (c.sel + delta + len(c.items)) % len(c.items)
}

// --- editor plumbing --------------------------------------------------------

// editorCursor returns the editor buffer and the byte offset of the cursor
// inside it.
func (q *queryModel) editorCursor() (string, int) {
	buf := q.ta.Value()
	lines := strings.Split(buf, "\n")
	row, col := q.cursorRowCol()
	return buf, offsetOf(lineStarts(lines), lines, row, col)
}

// wordAtCursor is the partial name an insertion replaces: the SQL name under
// the cursor, or for Redis the whole argument being typed, since a key holds
// characters no SQL name may.
func (q *queryModel) wordAtCursor() string {
	buf, cursor := q.editorCursor()
	if q.engine == config.Redis {
		return redisArgIn(redisLineAt(buf, cursor))
	}
	_, word := splitWordAtCursor(buf[:cursor])
	return word
}

// insertCompletion replaces the partial name before the cursor with text.
func (q *queryModel) insertCompletion(text string) {
	for range []rune(q.wordAtCursor()) {
		q.ta, _ = q.ta.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	q.ta.InsertString(text)
}

// completionText is what accepting a candidate types into the buffer: a SQL
// name is quoted when it cannot stand unquoted, while a Redis key and a
// keyword go in verbatim.
func (q *queryModel) completionText(kind completionKind, item string) string {
	switch kind {
	case completeTables:
		return quoteTable(q.engine, item)
	case completeColumns:
		return quoteIdent(q.engine, item)
	}
	return item
}

// --- key handling -----------------------------------------------------------

// startCompletion answers a Tab press in the editor. A single match goes
// straight into the buffer, several open the popup, and names the schema cache
// does not hold yet are fetched in the background.
func (m *model) startCompletion() tea.Cmd {
	q := &m.query
	if q.driver == nil || q.cache == nil {
		return nil
	}
	buf, cursor := q.editorCursor()
	return m.completeRequest(completionRequestAt(q.engine, buf, cursor))
}

// completeRequest resolves one request against the schema cache and updates the
// editor and the popup accordingly.
func (m *model) completeRequest(req completionRequest) tea.Cmd {
	q := &m.query
	if req.kind == completeNone {
		q.comp.close()
		return nil
	}
	all, need := q.completionCandidates(req)
	if len(need) > 0 {
		return m.loadForCompletion(need)
	}
	items := rankCandidates(all, req.word)
	switch len(items) {
	case 0:
		q.comp.close()
		m.status = "no completions here"
		return nil
	case 1:
		q.comp.close()
		q.insertCompletion(q.completionText(req.kind, items[0]))
		return nil
	}
	if shared := sharedPrefix(items, req.word); shared != "" {
		q.insertCompletion(shared)
	}
	q.comp = completionModel{open: true, all: all, items: items, listKind: req.kind}
	return nil
}

// loadForCompletion starts the catalog queries a completion is missing and
// leaves the popup showing a loading hint. Catalog queries use the session
// driver, so they wait for a run or the schema panel to let go of it.
func (m *model) loadForCompletion(need []schemaFetch) tea.Cmd {
	q := &m.query
	if q.running {
		q.comp.close()
		m.status = "query running – ctrl+x to cancel first"
		return nil
	}
	if q.schema.inflight {
		q.comp.close()
		m.status = "schema is loading – try again in a moment"
		return nil
	}
	cmds := []tea.Cmd{m.spinner.Tick}
	for _, f := range need {
		if f.listing {
			if q.cache.ClaimTables() {
				cmds = append(cmds, loadCompletionTablesCmd(q.cache))
			}
			continue
		}
		if q.cache.ClaimColumns(f.table) {
			cmds = append(cmds, loadCompletionColumnsCmd(q.cache, f.table))
		}
	}
	q.comp = completionModel{open: true, pending: true, hint: "loading schema..."}
	return tea.Batch(cmds...)
}

// updateCompletion handles a key while the popup is open and reports whether it
// consumed it. Keys that edit the buffer fall through to the editor, which
// narrows the list afterwards.
func (m *model) updateCompletion(msg tea.KeyMsg) bool {
	q := &m.query
	switch msg.String() {
	case "tab", "down", "ctrl+n":
		q.comp.move(1)
		return true
	case "shift+tab", "up", "ctrl+p":
		q.comp.move(-1)
		return true
	case "enter":
		m.acceptCompletion()
		return true
	case "esc":
		q.comp.close()
		return true
	case "backspace":
		return false
	}
	if msg.Type == tea.KeyRunes && !msg.Alt && q.staysInName(msg.Runes) {
		return false
	}
	// Anything else moves the cursor or runs a command: the popup no longer
	// describes where the cursor is.
	q.comp.close()
	return false
}

// staysInName reports whether typing these runes keeps the cursor inside the
// name being completed. A Redis key holds anything but whitespace, while a SQL
// name is limited to the bytes a name can hold.
func (q *queryModel) staysInName(runes []rune) bool {
	for _, r := range runes {
		switch {
		case q.engine == config.Redis:
			if r == ' ' || r == '\t' {
				return false
			}
		case r < 0x80 && !db.IsNameByte(byte(r)):
			return false
		}
	}
	return len(runes) > 0
}

// acceptCompletion replaces the partial name with the selected candidate.
func (m *model) acceptCompletion() {
	q := &m.query
	if q.comp.sel >= len(q.comp.items) {
		return
	}
	item, kind := q.comp.items[q.comp.sel], q.comp.listKind
	q.comp.close()
	q.insertCompletion(q.completionText(kind, item))
}

// narrowCompletion re-reads the partial name after the editor handled a key and
// keeps only the candidates that still match, closing the popup when none do.
func (q *queryModel) narrowCompletion() {
	if !q.comp.open || q.comp.pending {
		return
	}
	q.comp.items = rankCandidates(q.comp.all, q.wordAtCursor())
	q.comp.sel = 0
	if len(q.comp.items) == 0 {
		q.comp.close()
	}
}

// --- background loading -----------------------------------------------------

// completionSchemaMsg reports one finished catalog query started for
// completion.
type completionSchemaMsg struct{ err error }

func loadCompletionTablesCmd(c *schema.Cache) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		return completionSchemaMsg{err: c.LoadTables(ctx)}
	}
}

func loadCompletionColumnsCmd(c *schema.Cache, table string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		return completionSchemaMsg{err: c.LoadColumns(ctx, table)}
	}
}

// handleCompletionSchema finishes a Tab press that was waiting for the cache:
// once the names are in, the completion runs again against the buffer as it
// stands now, which is what opens the popup.
func (m *model) handleCompletionSchema(msg completionSchemaMsg) tea.Cmd {
	q := &m.query
	if !q.comp.pending {
		return nil
	}
	if msg.err != nil {
		q.comp.close()
		m.status = stErr.Render(msg.err.Error())
		return nil
	}
	if q.cache.Loading() {
		return nil // more names are still on the way
	}
	q.comp.close()
	if !q.editorFocused() {
		return nil // the cursor moved on while the names were loading
	}
	return m.startCompletion()
}

// refreshCompletionSchema drops the cached names so the next Tab reloads them.
func (m *model) refreshCompletionSchema() {
	if m.query.cache == nil {
		return
	}
	m.query.cache.Refresh()
	m.query.comp.close()
	m.status = stOK.Render("completion schema refreshed")
}

// --- rendering --------------------------------------------------------------

// completionView renders the popup, or an empty string when none is showing.
func (q *queryModel) completionView(width int, spin string) string {
	if !q.comp.open {
		return ""
	}
	inner := min(max(width-8, 20), maxStmtWidth)
	if q.comp.pending || len(q.comp.items) == 0 {
		loading := strings.TrimSpace(spin + " " + q.comp.hint)
		return stPopup.Render(stDim.Render(clip(loading, inner)))
	}

	var b strings.Builder
	start := 0
	if q.comp.sel >= completionMaxRows {
		start = q.comp.sel - completionMaxRows + 1
	}
	end := min(start+completionMaxRows, len(q.comp.items))
	for i := start; i < end; i++ {
		item := clip(q.comp.items[i], inner)
		if i == q.comp.sel {
			b.WriteString(stSelected.Render("> "+item) + "\n")
			continue
		}
		b.WriteString("  " + item + "\n")
	}
	if rest := len(q.comp.items) - end + start; rest > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  %d more", rest)) + "\n")
	}
	b.WriteString(stDim.Render(clip(completionHints, inner)))
	return stPopup.Render(b.String())
}

// completionHints is the key legend of the popup, shown both in it and in the
// footer while it is open.
const completionHints = "tab/↑↓ select · enter insert · esc cancel · typing narrows"
