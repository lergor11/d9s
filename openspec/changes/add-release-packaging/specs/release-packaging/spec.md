# release-packaging — Delta

## ADDED Requirements

### Requirement: Tagged Release Artifacts
Pushing a semver tag SHALL publish archives for darwin and linux on amd64 and
arm64, together with a checksum file and a changelog generated from the
commits since the previous tag.

#### Scenario: Tag produces artifacts
- **WHEN** `v0.2.0` is tagged and pushed
- **THEN** four archives and a checksum file are attached to the release, with
  a changelog listing the included commits

#### Scenario: Packaging breakage caught before tagging
- **WHEN** a pull request breaks the release build
- **THEN** the release-build check fails on that pull request without
  publishing anything

### Requirement: Version Stamping
The binary SHALL report the released version, the commit it was built from,
and the build date, stamped at link time rather than hardcoded. A build from
an untagged tree SHALL say so instead of claiming a release version.

#### Scenario: Released binary reports its version
- **WHEN** `d9s --version` runs on a binary from the `v0.2.0` release
- **THEN** it prints `v0.2.0` with the commit and build date

#### Scenario: Local build is marked as such
- **WHEN** `d9s --version` runs on a `make build` binary from a dirty tree
- **THEN** the output marks it as a development build

### Requirement: Installation Without Cloning
The project SHALL document installation for people who do not build from
source, covering a Homebrew tap and downloading a release archive with
checksum verification.

#### Scenario: Homebrew install documented
- **WHEN** a reader follows the README's Homebrew instructions
- **THEN** the documented command installs a working `d9s` binary
