# Test execution lanes

## Ordinary regression suite

`make test` runs `go test ./...` and Bash worker syntax checks. It does not
perform live package-manager operations, but it is not a no-subprocess or
no-socket suite. Some tests start loopback HTTP servers, run Bash workers,
cross-read artifacts with installed Portage Python, or exercise filesystem
transactions in temporary roots. Capability-dependent tests report skips when
the required executable is unavailable.

A sandbox that prohibits local listeners can fail this lane even when the code
is correct. Run it in a test environment that permits those listeners and
inspect skips before claiming real-worker coverage. Never redirect fixture
ROOT/VDB paths to the host to bypass an isolation failure.

`make test-race` checks internal packages. CLI concurrency also needs
`go test -race ./cmd/arise`. Coverage commands and dated measurements are in
[COVERAGE.md](COVERAGE.md); percentages from different lanes are not comparable.

## Live Portage comparisons

`make test-integration` enables the `live_portage` tag for host-tree reference
comparisons. `make bench-compare` runs the corresponding host-tool performance
lane. `make test-live-portage-compile` checks compilation without running these
comparisons. Record the repository/profile/VDB/configuration snapshot and
reference-tool versions alongside results.

These reference lanes inspect live metadata and run pretend/query commands;
they must not merge, uninstall, sync, or edit the host configuration. External
commands have class-specific deadlines, isolated process groups, and descendant
cleanup. The emerge reference adds `-news` to FEATURES to avoid news bookkeeping
while preserving dependency resolution.

Some tagged phase differentials build synthetic repositories and compare
normalized environments, images, and config protection in disposable roots.
They do not constitute a live-system upgrade or a fresh-stage3 acceptance run.

## Disposable-root mutation tests

The ordinary suite contains synthetic merge, journal, source/binary worker,
and recovery tests under temporary roots. Tagged comparisons extend that
coverage using Portage as a reference. Report which tests actually executed,
including required sandbox or filesystem capabilities, rather than treating
all capability skips as successful end-to-end validation.

An install/upgrade/unmerge smoke cycle must verify payloads, VDB, world,
configuration, and journal outcomes. Interruption tests must demonstrate retry
or rollback, not merely return an error.

## Privileged and fresh-system gates

Read-only reference capture requiring privilege is documented in
[REFERENCE_FIXTURES.md](../../misc/REFERENCE_FIXTURES.md). It is distinct from
ordinary tests and does not authorize live mutation.

The [fresh-stage3 runbook](../fresh-stage3.md) is a separate acceptance gate.
A passing Go suite, a disposable fixture, or an earlier development-host update
does not replace a recorded clean-image maintenance and recovery run.
