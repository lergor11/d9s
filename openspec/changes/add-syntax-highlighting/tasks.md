# Tasks — add-syntax-highlighting

## 1. Lexer
- [ ] 1.1 Export a token stream (kind + offsets) from the existing SQL lexer in `internal/db`
- [ ] 1.2 Redis tokenizer: command name vs arguments
- [ ] 1.3 Unit tests over the token stream

## 2. Rendering
- [ ] 2.1 Style each token kind via `styles.go`, verified on light and dark backgrounds
- [ ] 2.2 Gutter marker for the statement under the cursor
- [ ] 2.3 Keep rendering cost bounded for large buffers (highlight the visible window only)

## 3. Verification
- [ ] 3.1 Unit tests for the highlight-span builder
- [ ] 3.2 `make lint test` green
