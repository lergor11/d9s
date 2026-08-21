# Tasks — add-result-export

## 1. Implementation
- [x] 1.1 internal/export: WriteCSV/WriteJSON(Result, io.Writer); clipboard via pbcopy / wl-copy / xclip detection
- [x] 1.2 UI: with results focused, `e` opens a filename prompt (default ./results-<n>.csv; extension picks format), `y` copies CSV to clipboard; status line confirms
- [x] 1.3 Unit tests: CSV/JSON encoding incl. quoting, NULLs
- [x] 1.4 build/vet/test green
