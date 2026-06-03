#!/usr/bin/env bash
# Cut a release: stamp the CHANGELOG, bump the Version const, commit, tag, push.
# The tag push triggers .github/workflows/release.yml, which runs tests and
# creates the GitHub Release from the stamped CHANGELOG section.
#
# Usage:  scripts/release.sh v0.1.0
#         DRY_RUN=1 scripts/release.sh v0.1.0   # show changes, don't write/tag
#
# Versioning is independent SDK semver (NOT the CLI version, which lives in
# SupportedCLIVersion). See RELEASING.md.
set -euo pipefail

cd "$(dirname "$0")/.."

tag="${1:-}"
if [ -z "$tag" ]; then
	echo "usage: scripts/release.sh vMAJOR.MINOR.PATCH" >&2
	exit 2
fi
case "$tag" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*) echo "error: tag must look like v0.1.0 (got '$tag')" >&2; exit 2 ;;
esac
ver="${tag#v}"
today="$(date +%Y-%m-%d)"

# Preconditions.
if [ -n "$(git status --porcelain)" ]; then
	echo "error: working tree not clean; commit or stash first" >&2
	exit 1
fi
if git rev-parse "$tag" >/dev/null 2>&1; then
	echo "error: tag $tag already exists" >&2
	exit 1
fi
if ! grep -qE '^## Unreleased' CHANGELOG.md; then
	echo "error: CHANGELOG.md has no '## Unreleased' section to stamp" >&2
	exit 1
fi

echo "Releasing $tag ($today)"

apply() {
	# Stamp '## Unreleased' -> a fresh '## Unreleased' + '## [vX.Y.Z] - DATE'.
	perl -0pi -e "s/^## Unreleased\n/## Unreleased\n\n## [$tag] - $today\n/m" CHANGELOG.md
	# Bump the Version const.
	perl -pi -e "s/^const Version = \"[^\"]*\"/const Version = \"$ver\"/" claude.go
}

if [ "${DRY_RUN:-0}" = "1" ]; then
	echo "[dry-run] would stamp CHANGELOG and set Version=$ver, then:"
	echo "  git commit -am 'Release $tag'"
	echo "  git tag -a $tag -m 'Release $tag'"
	echo "  git push && git push origin $tag"
	exit 0
fi

apply

# Sanity: build + vet before tagging.
go build ./...
go vet ./... >/dev/null

git add CHANGELOG.md claude.go
git commit -m "Release $tag"
git tag -a "$tag" -m "Release $tag"
git push
git push origin "$tag"

echo "Pushed $tag. The Release workflow will run tests and publish the GitHub Release."
