# connection-config — Delta

## ADDED Requirements

### Requirement: Editing Connections From The Interface
The system SHALL let the user add, edit, and delete connections without
leaving d9s, writing changes back to the configuration file. Deletion SHALL
require confirmation naming the connection.

#### Scenario: Add a connection
- **WHEN** the user fills the add-connection form and saves
- **THEN** the connection appears in the list and is present in the config
  file after restart

#### Scenario: Delete asks first
- **WHEN** the user presses the delete key on a connection
- **THEN** a confirmation naming it appears, and nothing is removed unless the
  user confirms

### Requirement: Config Writing Preserves The File
Writing the configuration SHALL preserve existing comments, key order, and
formatting of untouched entries, and SHALL write atomically so an interrupted
save cannot truncate the file.

#### Scenario: Comments survive a save
- **WHEN** a config file carrying comments is saved after editing one
  connection
- **THEN** the comments and the other entries are unchanged

#### Scenario: Interrupted save keeps the old file
- **WHEN** writing fails midway
- **THEN** the previous configuration file remains intact

### Requirement: Form Refuses Plaintext Secrets Silently
The password field SHALL accept `op://` and `${ENV}` references; entering a
literal SHALL require an explicit confirmation that warns it will be stored in
plaintext.

#### Scenario: Literal password warns
- **WHEN** the user types a literal password and saves
- **THEN** a confirmation explains it will be stored in plaintext and offers
  the 1Password alternative
