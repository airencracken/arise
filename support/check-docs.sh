#!/usr/bin/env bash

# Do not enable global errexit, nounset, or pipefail modes. This checker
# accumulates independent failures explicitly so readers can see every issue.

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if ! source "$repo_dir/support/lib/error-handling.sh"; then
  printf 'error: cannot load support error-handling library\n' >&2
  exit 2
fi
status=0
info_output=
cleanup() {
  [[ -z $info_output ]] || rm -f -- "$info_output"
}
trap cleanup EXIT

for script in misc/arise-completion.bash internal/phaseproto/worker.sh support/lib/error-handling.sh support/fixtures/capture-reference-fixtures.sh support/perf/profile-p3-matrix.sh support/perf/profile-p3-world.sh; do
  if ! bash -n "$repo_dir/$script"; then
    status=1
  fi
done

if command -v makeinfo >/dev/null 2>&1; then
  if ! info_output=$(mktemp /tmp/arise-info.XXXXXX); then
    printf 'check-docs: cannot create temporary Info output\n' >&2
    status=1
  elif ! makeinfo --no-split -o "$info_output" "$repo_dir/arise.texi"; then
    status=1
  fi
  rm -f -- "$info_output"
  info_output=
else
  echo "check-docs: makeinfo unavailable; skipped Texinfo compilation" >&2
fi

if command -v mandoc >/dev/null 2>&1; then
  if ! mandoc -T lint "$repo_dir/arise.1"; then
    status=1
  fi
else
  echo "check-docs: mandoc unavailable; skipped man-page lint" >&2
fi

if ! git -C "$repo_dir" diff --check; then
  status=1
fi

exit "$status"
