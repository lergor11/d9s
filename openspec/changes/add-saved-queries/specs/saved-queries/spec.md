# saved-queries — Delta

## ADDED Requirements

### Requirement: Saving And Loading Named Queries
The system SHALL save the editor buffer under a user-supplied name, either
globally or scoped to the current connection, and SHALL load a saved query
into the editor without executing it. Saving under an existing name SHALL ask
before overwriting.

#### Scenario: Save and reload
- **WHEN** the user saves the buffer as "daily signups" and later loads it
- **THEN** the editor holds exactly the saved text and nothing has run

#### Scenario: Overwrite confirmed
- **WHEN** the user saves under a name that already exists
- **THEN** a confirmation appears, and the old text survives if declined

### Requirement: Human-Editable Storage
Saved queries SHALL live in a YAML file (default
`~/.config/d9s/queries.yaml`) that a person can edit by hand, with changes
picked up on the next open of the browser.

#### Scenario: Hand-edited file is picked up
- **WHEN** the user adds an entry to the file outside d9s and reopens the
  browser
- **THEN** the new entry is listed

### Requirement: Scoped Browsing
The browser SHALL list global queries together with those scoped to the
current connection, marking the scope, and SHALL filter by a case-insensitive
substring over names.

#### Scenario: Scope is visible
- **WHEN** the browser opens on connection `prod-pg`
- **THEN** global entries and `prod-pg` entries are listed with their scope
  marked, and entries scoped to other connections are not
