# result-grid — Delta

## ADDED Requirements

### Requirement: Column Sorting
The system SHALL sort the loaded rows by the selected column on demand,
toggling between ascending and descending, and SHALL indicate the sorted
column and direction in the header. Sorting SHALL order numeric columns
numerically and leave the underlying result unmodified.

#### Scenario: Sort toggles direction
- **WHEN** the user sorts by a column twice
- **THEN** the rows are ascending after the first and descending after the
  second, with the header marking the direction

#### Scenario: Numbers sort numerically
- **WHEN** an integer column holding 2, 10, and 9 is sorted ascending
- **THEN** the order is 2, 9, 10

### Requirement: Row Filtering
The system SHALL filter the displayed rows by a case-insensitive substring,
matching any column by default and only the selected column when one is
chosen. The status line SHALL report matched versus loaded row counts.

#### Scenario: Filter narrows rows
- **WHEN** the user filters by `err` on a 500-row result with 12 matches
- **THEN** 12 rows are shown and the status line reports 12 of 500

#### Scenario: Clearing restores everything
- **WHEN** the filter is cleared
- **THEN** all loaded rows are shown again in their previous order

### Requirement: Cell Inspector
The system SHALL open the selected cell in a full-screen scrollable view,
pretty-printing JSON values, with a key that copies the raw value to the
clipboard.

#### Scenario: Long value readable in full
- **WHEN** the user opens a cell holding 4 KB of text
- **THEN** the whole value is scrollable, and Esc returns to the grid

#### Scenario: JSON is pretty-printed
- **WHEN** the selected cell holds a JSON document
- **THEN** the inspector shows it indented, and copying yields the original
  raw text

### Requirement: Column Types In Headers
Result headers SHALL show each column's engine-reported data type beside its
name where the driver provides one.

#### Scenario: Types shown
- **WHEN** a postgres query returns an integer and a timestamp column
- **THEN** the headers show those type names alongside the column names
