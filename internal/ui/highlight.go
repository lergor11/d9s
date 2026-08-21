package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/db"
)

const (
	// gutterWidth is the marker column left of the line numbers.
	gutterWidth = 1
	// minEditorText is the narrowest text column the editor renders into, so a
	// very narrow terminal still shows something.
	minEditorText = 8
	// noWrapWidth is the line width handed to the textarea. The editor scrolls
	// sideways instead of wrapping, and the widget's cursor movement has to
	// agree with what is on screen, so it is told lines never wrap.
	noWrapWidth = 1 << 16
)

// --- statement scope --------------------------------------------------------

// statementSpan is the statement holding the cursor: the byte range it covers
// in the buffer and the tokens inside it.
type statementSpan struct {
	start, end int
	toks       []db.Token
}

// statementAt cuts a buffer's tokens at the top-level semicolons around the
// cursor and returns the statement holding it. size is the buffer length, so a
// last statement without a closing semicolon still has an end.
func statementAt(toks []db.Token, cursor, size int) statementSpan {
	span := statementSpan{end: size}
	first, last := 0, len(toks)
	for i, t := range toks {
		if !isPunct(t, ";") {
			continue
		}
		if t.End <= cursor {
			span.start, first = t.End, i+1
			continue
		}
		span.end, last = t.Start, i
		break
	}
	span.toks = toks[first:last]
	return span
}

// codeTokens drops the comments, leaving what the engine would actually run.
func codeTokens(toks []db.Token) []db.Token {
	out := make([]db.Token, 0, len(toks))
	for _, t := range toks {
		if t.Kind != db.TokenComment {
			out = append(out, t)
		}
	}
	return out
}

// tokensIn keeps the tokens lying inside the byte range.
func tokensIn(toks []db.Token, start, end int) []db.Token {
	var out []db.Token
	for _, t := range toks {
		if t.Start >= start && t.End <= end {
			out = append(out, t)
		}
	}
	return out
}

// lineBounds returns the byte range of the line the offset falls on, newline
// excluded.
func lineBounds(s string, offset int) (start, end int) {
	offset = min(max(offset, 0), len(s))
	start = strings.LastIndexByte(s[:offset], '\n') + 1
	end = len(s)
	if i := strings.IndexByte(s[offset:], '\n'); i >= 0 {
		end = offset + i
	}
	return start, end
}

// rowOf is the zero-based buffer row the byte offset falls on.
func rowOf(s string, offset int) int {
	return strings.Count(s[:min(max(offset, 0), len(s))], "\n")
}

// statementRows returns the first and last row of the statement the cursor sits
// in — the one a run starts from — and whether there is one to mark. SQL
// statements run between top-level semicolons and a Redis command is one line;
// rows carrying only blanks or comments belong to no statement.
func statementRows(engine config.EngineType, buf string, cursor int) (first, last int, ok bool) {
	code := statementCode(engine, buf, cursor)
	if len(code) == 0 && cursor > 0 {
		// The cursor rests just past a closing semicolon with nothing typed
		// after it; the statement it closed is still the one in view.
		code = statementCode(engine, buf, cursor-1)
	}
	if len(code) == 0 {
		return 0, 0, false
	}
	return rowOf(buf, code[0].Start), rowOf(buf, code[len(code)-1].End-1), true
}

// statementCode returns the tokens a run would send for the statement at the
// cursor: for SQL what sits between the surrounding top-level semicolons, for
// Redis the command line the cursor is on, comments excluded either way.
func statementCode(engine config.EngineType, buf string, cursor int) []db.Token {
	toks := db.Tokenize(engine, buf)
	if engine == config.Redis {
		start, end := lineBounds(buf, cursor)
		toks = tokensIn(toks, start, end)
	} else {
		toks = statementAt(toks, cursor, len(buf)).toks
	}
	return codeTokens(toks)
}

// --- highlight spans --------------------------------------------------------

// spanKind is how one run of buffer text is coloured.
type spanKind int

const (
	// spanPlain is an identifier, an operator, or anything else that reads
	// best in the terminal's own foreground.
	spanPlain spanKind = iota
	spanKeyword
	spanString
	spanNumber
	spanComment
)

// highlightSpan is a run of buffer bytes rendering in one style.
type highlightSpan struct {
	start, end int
	kind       spanKind
}

// highlightSpans returns the coloured runs of buf between the from and to byte
// offsets, in order and without gaps, so a renderer can walk them and lose no
// text. The whole buffer is lexed, because a string or a comment opened above
// the window still decides its colour, but only the window becomes spans.
func highlightSpans(engine config.EngineType, buf string, from, to int) []highlightSpan {
	from = min(max(from, 0), len(buf))
	to = min(max(to, from), len(buf))
	var out []highlightSpan
	at := from
	add := func(start, end int, kind spanKind) {
		if n := len(out) - 1; n >= 0 && out[n].kind == kind && out[n].end == start {
			out[n].end = end // one run of colour, however many tokens made it
			return
		}
		out = append(out, highlightSpan{start: start, end: end, kind: kind})
	}
	for _, t := range db.Tokenize(engine, buf) {
		if t.End <= from {
			continue
		}
		if t.Start >= to {
			break
		}
		start, end := max(t.Start, from), min(t.End, to)
		if start > at {
			add(at, start, spanPlain)
		}
		add(start, end, spanKindOf(t))
		at = end
	}
	if at < to {
		add(at, to, spanPlain)
	}
	return out
}

// spanKindOf is the colour a token renders in. Identifiers stay plain so the
// eye lands on the syntax around them, and a Redis command name reads like the
// SQL keyword it stands in for.
func spanKindOf(t db.Token) spanKind {
	switch t.Kind {
	case db.TokenString:
		return spanString
	case db.TokenNumber:
		return spanNumber
	case db.TokenComment:
		return spanComment
	case db.TokenCommand:
		return spanKeyword
	case db.TokenWord:
		if sqlKeywordSet[strings.ToUpper(t.Text)] {
			return spanKeyword
		}
	}
	return spanPlain
}

// syntaxStyle is the style one span kind renders in.
func syntaxStyle(kind spanKind) lipgloss.Style {
	switch kind {
	case spanKeyword:
		return stSyntaxKeyword
	case spanString:
		return stSyntaxString
	case spanNumber:
		return stSyntaxNumber
	case spanComment:
		return stSyntaxComment
	}
	return stSyntaxPlain
}

// --- row rendering ----------------------------------------------------------

// styledRun is a stretch of one rendered row sharing a colour.
type styledRun struct {
	text   string
	kind   spanKind
	cursor bool
}

// rowKinds maps every rune of a buffer line to the span kind colouring it.
// lineStart is the line's byte offset in the buffer the spans were built from.
func rowKinds(line string, lineStart int, spans []highlightSpan) []spanKind {
	runes := []rune(line)
	kinds := make([]spanKind, len(runes))
	offset, si := lineStart, 0
	for i, r := range runes {
		for si < len(spans) && spans[si].end <= offset {
			si++
		}
		if si < len(spans) && spans[si].start <= offset {
			kinds[i] = spans[si].kind
		}
		offset += len(string(r))
	}
	return kinds
}

// groupRuns groups neighbouring runes sharing a kind into runs. The rune at the
// cursor index, when there is one, stays in a run of its own so it can be drawn
// as the cursor.
func groupRuns(runes []rune, kinds []spanKind, cursor int) []styledRun {
	var out []styledRun
	for i := 0; i < len(runes); {
		if i == cursor {
			out = append(out, styledRun{text: string(runes[i]), kind: kinds[i], cursor: true})
			i++
			continue
		}
		j := i
		for j < len(runes) && kinds[j] == kinds[i] && j != cursor {
			j++
		}
		out = append(out, styledRun{text: string(runes[i:j]), kind: kinds[i]})
		i = j
	}
	return out
}

// renderRuns paints the runs of one row. The text itself is untouched: only
// colour is added around it.
func renderRuns(runs []styledRun) string {
	var b strings.Builder
	for _, r := range runs {
		style := syntaxStyle(r.kind)
		if r.cursor {
			style = style.Reverse(true)
		}
		b.WriteString(style.Render(r.text))
	}
	return b.String()
}

// --- the editor view --------------------------------------------------------

// editorView renders the editor: a gutter marking the statement a run starts
// from, line numbers, and the visible window of the buffer with its syntax
// coloured. It always renders exactly the height the layout gave the editor,
// and only the visible rows are turned into styled text.
//
// The textarea keeps the buffer and the cursor; only its rendering is ours,
// because the widget paints a whole line in one style.
func (q *queryModel) editorView() string {
	height := max(q.ta.Height(), 1)
	width := max(q.width, minEditorText+gutterWidth+3)
	buf := q.ta.Value()
	lines := strings.Split(buf, "\n")
	numWidth := len(strconv.Itoa(len(lines))) + 2
	textWidth := max(width-gutterWidth-numWidth, minEditorText)

	row, col := q.cursorRowCol()
	q.scrollEditor(row, col, height, textWidth)

	if buf == "" && q.ta.Placeholder != "" {
		return q.placeholderRows(height, numWidth, textWidth)
	}

	starts := lineStarts(lines)
	from := starts[min(q.edTop, len(lines)-1)]
	to := len(buf)
	if last := q.edTop + height; last < len(lines) {
		to = starts[last]
	}
	spans := highlightSpans(q.engine, buf, from, to)
	first, mark, marked := statementRows(q.engine, buf, offsetOf(starts, lines, row, col))

	var b strings.Builder
	for i := range height {
		if i > 0 {
			b.WriteString("\n")
		}
		line := q.edTop + i
		if line >= len(lines) {
			b.WriteString(strings.Repeat(" ", gutterWidth+numWidth+textWidth))
			continue
		}
		gutter := stGutter.Render("│")
		if marked && line >= first && line <= mark {
			gutter = stGutterActive.Render("┃")
		}
		number := stLineNumber
		cursor := -1
		if line == row {
			number = stCursorLineNumber
			if q.ta.Focused() {
				cursor = col - q.edLeft
			}
		}
		b.WriteString(gutter)
		b.WriteString(number.Render(fmt.Sprintf("%*d ", numWidth-1, line+1)))
		b.WriteString(q.rowText(lines[line], starts[line], spans, cursor, textWidth))
	}
	return b.String()
}

// rowText renders one buffer line, windowed to the columns on screen. cursor is
// the cursor's rune index within that window, or -1 when the cursor is
// elsewhere.
func (q *queryModel) rowText(line string, lineStart int, spans []highlightSpan, cursor, width int) string {
	runes := []rune(line)
	kinds := rowKinds(line, lineStart, spans)
	from := min(q.edLeft, len(runes))
	to := min(from+width, len(runes))
	vis, visKinds := slices.Clone(runes[from:to]), slices.Clone(kinds[from:to])
	if cursor < 0 || cursor > len(vis) {
		cursor = -1
	}
	if cursor == len(vis) {
		// The cursor sits past the last character of the line; it needs a cell
		// of its own to be drawn in.
		vis = append(vis, ' ')
		visKinds = append(visKinds, spanPlain)
	}
	return renderRuns(groupRuns(vis, visKinds, cursor)) + strings.Repeat(" ", max(0, width-len(vis)))
}

// placeholderRows renders the empty editor: the placeholder text, dimmed, with
// the cursor resting on its first character.
func (q *queryModel) placeholderRows(height, numWidth, textWidth int) string {
	cursor := -1
	if q.ta.Focused() {
		cursor = 0
	}
	runes := []rune(q.ta.Placeholder)
	if len(runes) > textWidth {
		runes = runes[:textWidth]
	}
	kinds := make([]spanKind, len(runes))
	for i := range kinds {
		kinds[i] = spanComment
	}
	var b strings.Builder
	b.WriteString(stGutter.Render("│"))
	b.WriteString(stCursorLineNumber.Render(fmt.Sprintf("%*d ", numWidth-1, 1)))
	b.WriteString(renderRuns(groupRuns(runes, kinds, cursor)))
	b.WriteString(strings.Repeat(" ", max(0, textWidth-len(runes))))
	for i := 1; i < height; i++ {
		b.WriteString("\n" + strings.Repeat(" ", gutterWidth+numWidth+textWidth))
	}
	return b.String()
}

// lineStarts returns the byte offset each line begins at.
func lineStarts(lines []string) []int {
	starts := make([]int, len(lines))
	at := 0
	for i, line := range lines {
		starts[i] = at
		at += len(line) + 1
	}
	return starts
}

// offsetOf is the byte offset of a rune position in the buffer.
func offsetOf(starts []int, lines []string, row, col int) int {
	if row < 0 || row >= len(lines) {
		return 0
	}
	runes := []rune(lines[row])
	return starts[row] + len(string(runes[:min(col, len(runes))]))
}

// cursorRowCol is the cursor's position as a buffer row and a rune column.
func (q *queryModel) cursorRowCol() (row, col int) {
	lines := strings.Split(q.ta.Value(), "\n")
	row = min(max(q.ta.Line(), 0), len(lines)-1)
	info := q.ta.LineInfo()
	col = min(max(info.StartColumn+info.ColumnOffset, 0), len([]rune(lines[row])))
	return row, col
}

// scrollEditor moves the visible window as little as it can to keep the cursor
// inside it.
func (q *queryModel) scrollEditor(row, col, rows, cols int) {
	if rows < 1 || cols < 1 {
		return
	}
	q.edTop = max(min(q.edTop, row), 0)
	if row >= q.edTop+rows {
		q.edTop = row - rows + 1
	}
	q.edLeft = max(min(q.edLeft, col), 0)
	if col >= q.edLeft+cols {
		q.edLeft = col - cols + 1
	}
}
