# Tasks — add-update-notice

## 1. Lookup
- [ ] 1.1 `internal/update`: fetch the latest release tag from the GitHub API
      (`releases/latest`, no auth), with a short timeout and a d9s User-Agent
- [ ] 1.2 Semver comparison that understands `v` prefixes and ignores
      pre-release/dev/non-semver versions rather than guessing
- [ ] 1.3 Honor `D9S_NO_UPDATE_CHECK` and skip the check for a `dev` build
- [ ] 1.4 Table-driven tests: comparison cases, opt-out, a fake HTTP server
      for the lookup, and silence on every failure path

## 2. Interface
- [ ] 2.1 Run the check once at startup as a `tea.Cmd`; deliver the result as
      a message, never blocking the UI
- [ ] 2.2 Header badge `update: vX.Y.Z available` when the release is newer;
      nothing at all otherwise
- [ ] 2.3 README: the badge, and how to disable the check

## 3. Verification
- [ ] 3.1 `make lint test` green
