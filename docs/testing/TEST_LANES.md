# Test execution lanes

Arise keeps host capabilities explicit. A missing privilege, network namespace
or Portage command must not silently weaken the routine correctness suite.

## Hermetic lane

`make test` or `go test ./...` runs without a live Portage tree, external
commands, loopback listeners or root access. HTTP behavior uses injected
transports; resolver parity uses immutable, fingerprinted fixtures. This is the
mandatory sandbox and ordinary CI gate.

## Live Portage lane

`make test-integration` runs read-only comparisons against the host Gentoo tree.
`make bench-compare` runs the host-tool performance comparisons. Both require
the `live_portage` build tag, which prevents them from entering the hermetic
lane accidentally. Every Portage, Python and Gentoolkit subprocess has a
measured command-class deadline, an isolated process group, descendant cleanup
and a bounded wait. Current ceilings are 10 seconds for `portageq`, 45 seconds
for `equery`, five minutes for emerge plans and 30 seconds for other tools. The
Go test command has a separate ten-minute suite deadline. These are safety
ceilings, not delays; commands return as soon as they complete.

Read-only emerge comparisons append `-news` to the caller's existing
incremental `FEATURES` value. This disables only post-plan news bookkeeping,
which otherwise attempts to adjust `/var/lib/gentoo/news` ownership and fails
inside a read-only sandbox. It does not alter candidate selection or dependency
resolution. Ordinary user emerge commands and non-comparison Arise execution
are unchanged.

`make test-live-portage-compile` verifies that the opt-in lane still compiles
without executing it.

Local coverage measurements, snapshot comparison rules and current improvement
priorities are documented in [`COVERAGE.md`](COVERAGE.md). Coverage tracking is
not connected to hosted CI.

These tests may inspect the live repository, profile and VDB, but must never
merge, uninstall, sync or modify configuration. Captured outputs are evidence,
not portable fixtures until sanitized and paired with an `arise state fixture`
snapshot.

The P4 environment and representative-package differentials create synthetic
repositories and build trees under the Go test temporary directory, run EAPI
7/8 phases under Portage and Arise, and compare normalized environments and
image trees. The config-protected fixture additionally merges into two isolated
temporary roots and compares preservation of the local file and creation of
the pending `._cfg0000_` update. These tests use the invoking user and group and
never target the live ROOT.

## Privileged read-only lane

Run the reference capture explicitly through `su` as documented in
[`misc/REFERENCE_FIXTURES.md`](../../misc/REFERENCE_FIXTURES.md). It records
pretend/query behavior across the privilege boundary; it performs no mutation.
This lane is not implied by the `live_portage` build tag.

## Disposable-root mutation lane

Real merge, removal and recovery validation belongs in a synthetic ROOT with
separate ROOT/SYSROOT/BROOT, repository, VDB, distfiles and configuration. It
must fail closed when the required isolation is unavailable. This lane remains
a P4 acceptance gate and is never part of `go test ./...`.

Verified mutation plans can be saved without shell redirection using
`--save-plan NAME`; names resolve beneath `${PORTAGE_TMPDIR}/arise/plans` by
default and may be redirected with `--plan-dir`. An explicit path is also
accepted. A later state-bound command may use `--approve-plan NAME_OR_PATH`
instead of copying the embedded SHA-256. Arise rereads the saved JSON, requires
it to be complete and verified, and compares its digest with the freshly
resolved plan; the file is authorization evidence, not an instruction stream.
`--approve-plan-sha256` remains available for scripts.
