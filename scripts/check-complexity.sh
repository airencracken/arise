#!/usr/bin/env bash

fail() {
	printf 'check-complexity: %s\n' "$*" >&2
	exit 1
}

analyzer=${GOCYCLO:-gocyclo}
command -v "$analyzer" >/dev/null 2>&1 ||
	fail "gocyclo 0.6.0 is required; set GOCYCLO to its executable path"

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd) ||
	fail "cannot locate repository root"

average=$(
	"$analyzer" -ignore '_test\.go$' -avg-short "$root/cmd" "$root/internal" "$root/misc" |
		tail -1
) || fail "cannot calculate average complexity"
[[ $average =~ ^[0-9]+([.][0-9]+)?$ ]] || fail "invalid average complexity: $average"

over_fifty=$(
	"$analyzer" -ignore '_test\.go$' -over 50 "$root/cmd" "$root/internal" "$root/misc" 2>/dev/null |
		wc -l
) || fail "cannot count functions over complexity 50"
over_fifty=${over_fifty//[[:space:]]/}

if ! awk -v actual="$average" 'BEGIN { exit !(actual <= 7.89) }'; then
	fail "average complexity $average exceeds the 7.89 ratchet"
fi
if (( over_fifty > 20 )); then
	fail "$over_fifty functions exceed complexity 50; ratchet allows 20"
fi
if ! "$analyzer" -ignore '_test\.go$' -over 221 "$root/cmd" "$root/internal" "$root/misc" >/dev/null 2>&1; then
	fail "a production function exceeds the complexity ceiling of 221"
fi

printf 'Cyclomatic complexity: average %s; %s functions over 50; maximum at most 221.\n' \
	"$average" "$over_fifty"
