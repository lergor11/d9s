# result-export — Delta

## ADDED Requirements

### Requirement: Export Result To File
The system SHALL export the focused statement's result to CSV or JSON, chosen
by the file extension of a user-provided path (default `./results-<n>.csv`).
CSV MUST quote per RFC 4180; JSON MUST be an array of objects keyed by column
name; NULLs export as empty CSV fields / JSON null.

#### Scenario: CSV export
- **WHEN** the user presses `e` on a result and accepts `results-1.csv`
- **THEN** the file contains a header row and all rows, and the status line
  confirms the path and row count

#### Scenario: JSON export
- **WHEN** the user enters a path ending in `.json`
- **THEN** an array of objects keyed by column names is written

### Requirement: Copy Result To Clipboard
The system SHALL copy the focused result as CSV to the system clipboard using
`pbcopy` (macOS) or `wl-copy`/`xclip` (Linux), reporting an error when no
clipboard tool is available.

#### Scenario: Copy on macOS
- **WHEN** the user presses `y` on a result on macOS
- **THEN** the clipboard contains the CSV and the status line confirms
