# Releasing

This module is released as git tags following [semver](https://semver.org).
`go get github.com/shindakun/agent-sdk-go@vX.Y.Z` resolves tags directly — there
is no package registry or build artifact to publish.

## Versioning

Releases use **independent SDK semver** — the version tracks *this Go SDK's* API
surface, not the bundled CLI version:

- **patch** (`v0.1.0` → `v0.1.1`) — bug fixes; a CLI bump that needs no code
  change can ride along here.
- **minor** (`v0.1.0` → `v0.2.0`) — new, backward-compatible API.
- **major** — breaking changes. While `v0`, breaking changes may land in a minor
  bump. Going to `v2.0.0+` later requires the `/v2` module-path suffix in
  `go.mod`.

CLI compatibility is recorded separately: `SupportedCLIVersion` in
[claude.go](claude.go) and the per-bump CHANGELOG entries.

## Cutting a release

1. Make sure `## Unreleased` in [CHANGELOG.md](CHANGELOG.md) lists everything in
   the release, and the working tree is clean.
2. Run the helper with the new tag:

   ```bash
   scripts/release.sh v0.2.0          # or DRY_RUN=1 to preview
   ```

   It stamps `## Unreleased` → `## [v0.2.0] - <date>` (leaving a fresh
   `## Unreleased`), bumps the `Version` const, builds/vets, then commits, tags,
   and pushes.
3. The push of the `v*` tag triggers
   [`.github/workflows/release.yml`](.github/workflows/release.yml), which runs
   the test matrix and creates the GitHub Release with that CHANGELOG section as
   the notes. `v0.*` and pre-release tags are marked as pre-releases.

## Manual fallback

If you'd rather not use the script:

```bash
# edit CHANGELOG.md: ## Unreleased -> ## [v0.2.0] - YYYY-MM-DD (+ new Unreleased)
# edit claude.go:    const Version = "0.2.0"
git commit -am "Release v0.2.0"
git tag -a v0.2.0 -m "Release v0.2.0"
git push && git push origin v0.2.0
```
