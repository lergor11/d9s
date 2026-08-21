# Tasks — add-syntax-highlighting

## 1. Lexer
- [x] 1.1 Export a token stream (kind + offsets) from the existing SQL lexer in `internal/db`
      — `db.Tokenize` already carried kinds and offsets; it now also emits
        `TokenComment` and `TokenNumber` instead of dropping or folding them
- [x] 1.2 Redis tokenizer: command name vs arguments
      — `db.Tokenize` lexes Redis too: `TokenCommand` at the start of a line,
        quoted arguments whole, `#` lines as comments
- [x] 1.3 Unit tests over the token stream
      — `internal/db/token_test.go`; `Split` and `Destructive` keep their own
        tests and run on the same tokenizer

## 2. Rendering
- [x] 2.1 Style each token kind via `styles.go`, verified on light and dark backgrounds
      — mid-tone 256-colour values from the existing palette, identifiers left
        in the terminal's own foreground; a test pins them distinct and in range
- [x] 2.2 Gutter marker for the statement under the cursor
- [x] 2.3 Keep rendering cost bounded for large buffers (highlight the visible window only)
      — only the window is lexed (with a bounded look-back) and styled;
        `BenchmarkEditorView` holds ~0.4 ms/frame at 100 lines against the stock
        widget's 1.6 ms, and 4.5 ms at 10 000 lines against its 127 ms

## 3. Verification
- [x] 3.1 Unit tests for the highlight-span builder
      — plus the gutter, the run grouper, the scrolling window, and the
        buffer-text-unchanged and cursor-stays-put scenarios from the spec
- [x] 3.2 `make lint test` green

## Notes
- bubbles' textarea paints a whole line in one style, so the editor keeps the
  widget for the buffer, the cursor and every key binding, and renders its own
  view (`internal/ui/highlight.go`). Lines no longer soft-wrap: the editor
  scrolls sideways instead, which is what keeps the widget's cursor movement
  and what is on screen in agreement.
