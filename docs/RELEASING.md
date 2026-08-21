# Releasing d9s

Releases are cut by pushing a semver tag. The `Release` workflow
(`.github/workflows/release.yml`) runs GoReleaser, which builds darwin and
linux binaries for amd64 and arm64, attaches the archives and `checksums.txt`
to a GitHub release with a changelog generated from the commits since the
previous tag, and pushes a Homebrew cask to `andreim/homebrew-tap`.

## One-time setup

Both of these must exist **before the first tag**. GoReleaser publishes the
GitHub release first and the cask last, so a missing tap or token fails the run
*after* the release is already public — you would have to delete the release
and the tag to retry cleanly.

1. **Create the tap repository.** It does not exist yet. Create a public
   `andreim/homebrew-tap` with a `main` branch and at least one commit (an
   empty repository has no branch for GoReleaser to push onto). The `Casks/`
   directory is created by the first release.
2. **Create the tap token.** The default `GITHUB_TOKEN` cannot push to another
   repository. Generate a PAT with `contents: write` on `andreim/homebrew-tap`
   and add it to the `d9s` repository as the secret
   `HOMEBREW_TAP_GITHUB_TOKEN`.

To check both without cutting a release, push a throwaway pre-release tag such
as `v0.0.1-rc1`: `prerelease: auto` marks it as a pre-release, the cask is
still pushed, and you can delete the release and the tag afterwards.

## Cutting a release

1. **Confirm CI is green on `main`.** All four jobs must pass, including
   `release-build`, which runs `goreleaser release --snapshot` on every pull
   request and is the check that catches packaging breakage.
2. **Update anything version-related.** The version in the binary is stamped
   from the tag, so there is no version constant to edit. Do check that the
   README's install instructions still name the right commands, and that any
   new user-visible flag or key binding is reflected in the `--help` text in
   `cmd/d9s/main.go`.
3. **Run the release build locally.** `make snapshot` (or
   `goreleaser release --snapshot --clean`) must succeed. Inspect
   `dist/` and confirm four archives and a `checksums.txt` are present.
4. **Run the pre-commit checks.** `make lint test` must be clean.
5. **Tag and push.**

   ```sh
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin v0.2.0
   ```

   The tag must match `vMAJOR.MINOR.PATCH` (optionally with a `-suffix`); the
   release workflow does not trigger on anything else.
6. **Confirm the artifacts.** On the GitHub release page, check that there are
   four `.tar.gz` archives (darwin and linux, amd64 and arm64), a
   `checksums.txt`, and a changelog listing the commits since the previous tag.
7. **Confirm the tap.** `andreim/homebrew-tap` should have a new commit adding
   or updating `Casks/d9s.rb`. Verify the install end to end:

   ```sh
   brew untap andreim/tap 2>/dev/null
   brew install andreim/tap/d9s
   d9s --version
   ```

   The version printed must be the tag you just pushed.

## Version stamping

`main.version`, `main.commit`, and `main.date` in `cmd/d9s/main.go` are set at
link time.

- A plain `go build` leaves the defaults: `dev`, `none`, `unknown`.
- `make build` stamps `dev-<short commit>`, plus `-dirty` when the working tree
  has uncommitted changes. A local build never claims a release version.
- GoReleaser stamps the tag (`v0.2.0`), the full commit, and the build date.

## Notes and limitations

- **The tap only serves macOS.** GoReleaser writes `on_linux` blocks into the
  generated cask, but Homebrew does not install casks on Linux, so
  `brew install andreim/tap/d9s` works on macOS only. Linux users install from
  the release archive. Keep that distinction in the README.
- **The binaries are not signed or notarized.** The cask strips the
  `com.apple.quarantine` attribute on install so the binary runs; a user who
  downloads the archive directly on macOS has to do that themselves, or clear
  it through System Settings on first run.
- **There is no `LICENSE` file in the repository.** `brew audit` wants one and
  the cask currently declares no license. Add one before promoting the tap.

## If a release goes wrong

GoReleaser does not roll back. To retry a tag, delete the GitHub release and
the tag on both the remote and locally, then re-tag:

```sh
gh release delete v0.2.0 --yes
git push origin :refs/tags/v0.2.0
git tag -d v0.2.0
```

If the cask was already pushed, revert that commit in the tap repository too.
