# Tasks — add-release-packaging

## 1. Build metadata
- [x] 1.1 `version`, `commit`, `date` variables set via `-ldflags`; `make build` marks development builds
- [x] 1.2 `--version` prints all three

## 2. GoReleaser
- [x] 2.1 `.goreleaser.yaml`: darwin/linux × amd64/arm64, archives, checksums, changelog
- [x] 2.2 Release workflow on tags; snapshot build on pull requests
- [x] 2.3 Homebrew tap formula

## 3. Documentation
- [x] 3.1 README install section: Homebrew, release archive with checksum verification, build from source
- [x] 3.2 Release checklist in the repository

## 4. Verification
- [x] 4.1 `goreleaser release --snapshot --clean` succeeds locally
- [ ] 4.2 `make lint test` green
