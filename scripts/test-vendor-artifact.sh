#!/usr/bin/env bash

fail() {
	printf '%s\n' "test-vendor-artifact: $*" >&2
	exit 1
}

version=${VERSION:-}
source_epoch=${SOURCE_DATE_EPOCH:-}
[[ -n $version && -n $source_epoch ]] || fail "VERSION and SOURCE_DATE_EPOCH are required"

work=$(mktemp -d "${TMPDIR:-/tmp}/arise-vendor-test.XXXXXXXX") ||
	fail "cannot create test directory"
cleanup() {
	rm -rf -- "$work"
}
trap cleanup EXIT HUP INT TERM

for run in one two; do
	OUTPUT_DIR="$work/$run" VERSION="$version" SOURCE_DATE_EPOCH="$source_epoch" \
		bash scripts/build-vendor-artifact.sh ||
		fail "artifact build $run failed"
done

first="$work/one/arise-$version-vendor.tar.xz"
second="$work/two/arise-$version-vendor.tar.xz"
cmp -s "$first" "$second" || fail "artifact is not reproducible"

mkdir -p "$work/source" "$work/modcache" "$work/gocache" ||
	fail "cannot create offline test directories"
git ls-files --cached --others --exclude-standard -z >"$work/source-files" ||
	fail "cannot list source files"
tar --null -T "$work/source-files" -cf "$work/source.tar" ||
	fail "cannot archive source tree"
tar -xf "$work/source.tar" -C "$work/source" ||
	fail "cannot export source tree"
tar -xJf "$first" -C "$work/source" ||
	fail "cannot unpack vendor artifact"

commit=$(git rev-parse HEAD) || fail "cannot determine source commit"
cd "$work/source" || fail "cannot enter exported source tree"
GOMODCACHE="$work/modcache" GOCACHE="$work/gocache" GOPROXY=off \
	go run -mod=vendor ./cmd/arise-vendor-manifest \
	-mode=verify -root . -output arise-vendor-manifest.json \
	-version "$version" -commit "$commit" ||
	fail "manifest verification failed"
GOMODCACHE="$work/modcache" GOCACHE="$work/gocache" GOPROXY=off GOFLAGS=-buildvcs=false \
	go test -mod=vendor ./... -count=1 -timeout 120s ||
	fail "offline tests failed"

printf '%s\n' "Vendor artifact is reproducible, verified, and builds offline."
