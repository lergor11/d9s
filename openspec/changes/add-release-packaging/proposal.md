# Change: Ship releases people can install

## Why
The only way to get d9s today is to clone the repository and run `make build`.
There are no versioned binaries, no checksums, and `--version` reports a
hardcoded development string regardless of what was built.

## What Changes
- GoReleaser builds darwin and linux binaries for amd64 and arm64 on a tag,
  with checksums and a generated changelog
- The version, commit, and build date are stamped into the binary at link time
- A Homebrew tap formula and installation instructions for the binaries
- A CI job that runs the release build on pull requests without publishing, so
  packaging breaks are caught before tagging

## Impact
- Affected specs: release-packaging (new)
- Affected code: `.goreleaser.yaml`, `.github/workflows/`, `cmd/d9s/main.go`
  (version variables), README
