# secret-resolution — Delta

## ADDED Requirements

### Requirement: Browsing 1Password Items
The system SHALL list vaults, the items in a vault, and the fields of an item
through the 1Password CLI, and SHALL produce the corresponding
`op://vault/item/field` reference for the user's selection without displaying
the secret value.

#### Scenario: Reference built from a selection
- **WHEN** the user picks vault `Infra`, item `prod-pg`, field `password`
- **THEN** `op://Infra/prod-pg/password` is written into the field being
  edited and no secret value is shown

#### Scenario: Locked vault explained
- **WHEN** 1Password is locked
- **THEN** the picker says so and tells the user to unlock it, rather than
  showing a CLI exit status

### Requirement: Reference Validation
The system SHALL check whether an `op://` reference resolves, reporting
success or the CLI's error without printing the secret.

#### Scenario: Bad reference reported
- **WHEN** the user validates `op://Infra/typo/password`
- **THEN** the error names the reference and the CLI's reason, and no secret
  is printed

#### Scenario: Good reference confirmed
- **WHEN** the user validates a reference that resolves
- **THEN** the interface confirms it resolves without revealing the value
