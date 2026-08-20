# Tasks — add-result-export

## 1. Implementation
- [ ] 1.1 internal/export: WriteCSV/WriteJSON(Result, io.Writer); clipboard via pbcopy / wl-copy / xclip detection
- [ ] 1.2 UI: with results focused, `e` opens a filename prompt (default ./results-<n>.csv; extension picks format), `y` copies CSV to clipboard; status line confirms
- [ ] 1.3 Unit tests: CSV/JSON encoding incl. quoting, NULLs
- [ ] 1.4 build/vet/test green
