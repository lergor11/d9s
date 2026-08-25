# update-notice — Delta

## ADDED Requirements

### Requirement: New Version Badge
The system SHALL check the project's latest published release once at
startup, off the UI goroutine, and SHALL show the available version in the
header when it is newer than the running build. The badge SHALL name the
version, and the interface SHALL behave identically whether the check has
finished, failed, or not run.

#### Scenario: A newer release exists
- **WHEN** d9s v0.2.0 starts and the latest release is v0.3.0
- **THEN** the header shows `update: v0.3.0 available` once the check
  completes, and the interface is usable before it does

#### Scenario: Running the latest
- **WHEN** the running version equals the latest release
- **THEN** no badge is shown

### Requirement: The Check Fails Silently
A failed check SHALL produce no badge, no error, and no delay, whether it
failed offline, rate-limited, timed out, or on an unparsable response.

#### Scenario: Offline start
- **WHEN** d9s starts with no network
- **THEN** the interface opens as always and nothing mentions updates

### Requirement: Opting Out
The system SHALL NOT contact the release host when `D9S_NO_UPDATE_CHECK` is
set to a non-empty value, when the running version is a dev build, or when
the version is not a semver release. CLI subcommands SHALL never check.

#### Scenario: Disabled by environment
- **WHEN** d9s starts with `D9S_NO_UPDATE_CHECK=1`
- **THEN** no request is made to the release host

#### Scenario: Dev build
- **WHEN** a build with version `dev` starts
- **THEN** no request is made and no badge can appear

#### Scenario: Scripting stays offline
- **WHEN** `d9s query` or any other subcommand runs
- **THEN** no update check happens
