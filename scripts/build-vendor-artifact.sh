#!/usr/bin/env bash

fail() {
	printf '%s\n' "build-vendor-artifact: $*" >&2
	exit 1
}

version=${VERSION:-}
output_dir=${OUTPUT_DIR:-dist}
source_epoch=${SOURCE_DATE_EPOCH:-}

[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([_-][A-Za-z0-9.]+)?$ ]] ||
	fail "VERSION must be a release version"
[[ $source_epoch =~ ^[1-9][0-9]*$ ]] ||
	fail "SOURCE_DATE_EPOCH must be a positive integer"

command -v go >/dev/null 2>&1 || fail "go is required"
command -v git >/dev/null 2>&1 || fail "git is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

commit=$(git rev-parse HEAD) || fail "cannot determine source commit"
git diff --quiet -- go.mod go.sum ||
	fail "go.mod or go.sum has uncommitted changes"
git diff --cached --quiet -- go.mod go.sum ||
	fail "go.mod or go.sum has staged changes"
stage=$(mktemp -d "${TMPDIR:-/tmp}/arise-vendor.XXXXXXXX") ||
	fail "cannot create staging directory"
cleanup() {
	rm -rf -- "$stage"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$output_dir" || fail "cannot create $output_dir"
go mod download || fail "cannot download modules"
go mod verify || fail "module verification failed"
GOCACHE="$stage/go-cache" go mod vendor -o "$stage/vendor" ||
	fail "cannot construct vendor tree"
cp go.mod go.sum "$stage/" || fail "cannot stage module metadata"

GOCACHE="$stage/go-cache" go run ./cmd/arise-vendor-manifest \
	-mode=create \
	-root "$stage" \
	-version "$version" \
	-commit "$commit" \
	-source-date-epoch "$source_epoch" \
	-output "$stage/arise-vendor-manifest.json" ||
	fail "cannot create provenance manifest"

archive="$output_dir/arise-$version-vendor.tar.xz"
XZ_OPT='-T1 -9' tar --sort=name --mtime="@$source_epoch" \
	--owner=0 --group=0 --numeric-owner \
	-C "$stage" -cJf "$archive" vendor arise-vendor-manifest.json ||
	fail "cannot create $archive"

printf '%s\n' "Created $archive"
