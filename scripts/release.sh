#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Builds, packages and checksums the release downloads in dist/ for the tag
# on the current commit. Called by `make release`; an optional argument is
# the VERSION given on the make command line, which must equal that tag.
# Never touches GitHub: it only prints the upload command.

set -eu

PKG=github.com/martinhammer/tickcli
TARGETS="linux/amd64 linux/arm64 linux/arm/7 darwin/amd64 darwin/arm64 windows/amd64"

fail() { echo "$*" >&2; exit 1; }

cd "$(dirname "$0")/.."

# 1. Checks: a vX.Y.Z tag on this commit, a clean tree, a matching VERSION.
tag=$(git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null) || tag=
echo "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
	fail 'Not on a release tag: tag the commit first (git tag -a v0.1.0 -m "…")'
[ -z "$(git status --porcelain)" ] ||
	fail 'Uncommitted changes: commit or stash them first'
[ -z "${1:-}" ] || [ "$1" = "$tag" ] ||
	fail "VERSION=$1 doesn't match the tag $tag"
version=$tag
plain=${version#v}

# 2. Vet, test and, if installed, staticcheck.
echo "go vet ./..."
go vet ./...
echo "go test ./..."
go test ./...
if command -v staticcheck >/dev/null 2>&1; then
	echo "staticcheck ./..."
	staticcheck ./...
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$@"; }
else
	sha256() { shasum -a 256 "$@"; }
fi

# 3 and 4. Build each target into its own folder and pack it.
rm -rf dist
mkdir -p dist
for t in $TARGETS; do
	os=${t%%/*}
	rest=${t#*/}
	arch=${rest%%/*}
	arm=
	[ "$rest" = "$arch" ] || arm=${rest#*/}
	name=tickcli_${plain}_${os}_${arch}${arm:+v$arm}
	bin=tickcli
	[ "$os" = windows ] && bin=tickcli.exe

	echo "build $name"
	mkdir "dist/$name"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=$arm \
		go build -trimpath -ldflags "-s -w -X $PKG/internal/cli.version=$version" \
		-o "dist/$name/$bin" ./cmd/tickcli
	cp LICENSE README.md "dist/$name/"
	if [ "$os" = windows ]; then
		(cd dist && zip -qr "$name.zip" "$name")
	else
		tar -C dist -czf "dist/$name.tar.gz" "$name"
	fi
	rm -rf "dist/$name"
done

# 5. Checksums over the archives.
(cd dist && sha256 *.tar.gz *.zip > SHA256SUMS)

# The changelog section for the release notes, headed "## [0.1.0] - date"
# (or "## 0.1.0 …"). dist/*.tar.gz, *.zip and SHA256SUMS are the uploads,
# so this file isn't one of them.
notes=dist/NOTES.md
awk -v v="$plain" '
	/^## / { if (on) exit; h = $2; gsub(/\[|\]/, "", h); on = (h == v); next }
	on
' CHANGELOG.md | sed -e '/./,$!d' > "$notes"

# 6. What was made and what to do next.
echo
echo "Made in dist/:"
(cd dist && ls -l *.tar.gz *.zip SHA256SUMS)
echo
if [ -s "$notes" ]; then
	echo "Release notes from CHANGELOG.md ($plain) are in $notes."
else
	rm -f "$notes"
	notes=NOTES.md
	echo "No section \"## [$plain] - <date>\" in CHANGELOG.md: write the release notes to $notes first."
fi
echo "Upload to the release:"
echo "  gh release create $version dist/*.tar.gz dist/*.zip dist/SHA256SUMS --title \"Tickcli $version\" --notes-file $notes"
