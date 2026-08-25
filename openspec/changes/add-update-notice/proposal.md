# Change: Notify about a new d9s version

## Why
d9s is installed as a static binary through Homebrew or a curl of a GitHub
release, so nothing tells the user a newer version exists. A stale client
quietly misses fixes, and "what version am I on, is there a newer one" is a
question the interface can answer in one glance.

## What Changes
- On startup the interface checks the latest GitHub release of `lergor11/d9s`
  in the background and, when it is newer than the running build, shows an
  unobtrusive badge in the header: `update: v0.3.0 available`
- The check never blocks or delays the interface: it runs off the UI
  goroutine with a short timeout, and any failure (offline, rate-limited,
  proxy) is silent — no badge, no error
- Dev builds (`version = "dev"`) and non-semver versions never check and
  never show the badge
- `D9S_NO_UPDATE_CHECK=1` disables the check entirely, because a phone-home
  on start must be easy to turn off; the README documents both the badge and
  the switch
- `d9s -version` output is unchanged; the CLI subcommands do not check —
  scripts should not open network connections they did not ask for

## Impact
- Affected specs: update-notice (new)
- Affected code: new `internal/update` (release lookup + semver compare),
  `internal/ui` (header badge), `cmd/d9s` (wiring the running version in),
  README
