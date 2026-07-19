# Development checkpoint — 2026-07-18

> Historical checkpoint. Superseded by
> [`CHECKPOINT_P3_P6_2026-07-18.md`](CHECKPOINT_P3_P6_2026-07-18.md); statements
> below describe an earlier tree from the same day.

This is an honest state record for commit and push. `PUNCHLIST.md` remains the
authoritative dependency graph; this document summarizes what the current tree
actually proves and, equally importantly, what it does not prove.

## Status at this checkpoint

- P1 package-state modeling is complete by its current acceptance criteria:
  19 complete, no partial, blocked, or open entries.
- P2 Portage configuration/profile evaluation is complete by its current
  acceptance criteria: 23 complete, no partial, blocked, or open entries.
- P3 resolver correctness is not complete: 19 complete, 16 partial, 3 blocked,
  and 9 open entries.
- P4 ebuild execution is not complete: 2 complete, 9 partial, 1 blocked, and
  13 open entries.
- P5 fetching is useful but incomplete: 5 complete, 4 partial, and 5 open.
- P6-P8 transaction scheduling and live package management remain substantially
  unfinished and block calling Arise a safe live Portage replacement.

Counts describe punch-list entries, not weighted completion percentages. A
small open transaction item can carry more risk than many completed parser
items.

## What is demonstrated

- Repository/VDB ingestion, indexed search, installed-package queries, Portage
  configuration/profile evaluation, and deterministic resolver snapshots have
  broad unit and differential coverage.
- P1 and P2 satisfy their current documented gates.
- Dependency classes, EAPI-aware grammar, ROOT/SYSROOT/BROOT placement,
  repository constraints, USE dependencies, REQUIRED_USE, blockers, slots,
  grouped alternatives, provider selection, and whole-plan verification have
  substantial permanent coverage. P3 nevertheless remains open.
- Repository `eapis-banned`/`eapis-deprecated` policy is preserved through
  metadata and snapshots. Banned candidates are ineligible; deprecated
  candidates warn; installed historical records remain available for recovery.
- Backtracking uses one bounded decision ledger. Later conflicts can replay
  earlier any-of, provider, and repository-available version decisions.
  Multiple remaining replay alternatives can run concurrently when `Jobs > 1`;
  commitment follows deterministic preference order rather than completion
  order. JSON plans include decision and branch-evaluation records suitable for
  future debugging visualizations.
- Source `--fetchonly` uses Manifest verification, reports transfer progress,
  and uses fetch-specific plan wording. It is the only intentionally enabled
  non-pretend package workflow at this milestone.
- The phase protocol can execute a declared EAPI 7/8 subset in disposable roots.
  A live Signal Desktop source archive passed Manifest-verified unpack,
  prepare, and install-image testing. This is not equivalent to a safe live
  package installation.
- Stored benchmarks cover indexed queries and resolver workloads, including
  `@system` and captured `@world` modes. Claims are limited to the recorded
  machine/snapshot and equivalent workloads.

## What is not demonstrated

- Arise is not safe to use as a general live installer, updater, uninstaller,
  depcleaner, or system-recovery package manager.
- P3 does not yet have general nested speculative search, cancellation,
  adaptive scheduling, complete Portage atom/slot/provider parity, or a
  portable conflict-free `@world` differential fixture.
- Concurrent replay is a bounded second-pass optimization after a failed
  choice. It is not yet a fully parallel dependency solver.
- P4 does not provide the complete EAPI helper/phase ABI. Current execution
  support is intentionally fail-closed and limited to the declared subset.
- The operation journal, crash recovery, CONFIG_PROTECT transaction behavior,
  dependency-correct live scheduler, and complete merge/unmerge lifecycle are
  unfinished.
- Live tagged Gentoo tests depend on host state and are not part of the normal
  portable `make test` gate. The damaged development laptop's `@world` plan is
  evidence and a debugging corpus, not a portable correctness oracle.
- Test-suite and benchmark coverage are tracked separately. Neither line nor
  benchmark coverage is presently a release-quality completeness metric.

## Validation performed for this checkpoint

- `make test` passes for all normal packages.
- Focused resolver concurrency tests pass under Go's race detector.
- `bash -n internal/phaseproto/worker.sh` passes through the normal test gate.
- `git diff --check` is clean.

The Go command emits a harmless warning when attempting to update a read-only
module download stat-cache entry in this environment; compilation and tests
still complete successfully.

## Immediate next work

1. Extend nested conflict-directed search without weakening deterministic
   minimal-change selection.
2. Add cancellation and measured scheduling policy for speculative branches.
3. Continue closing P3 parity gaps and promote sanitized live plan captures into
   portable differential fixtures.
4. Continue the P4 helper/phase ABI before attempting a live package merge.
5. Build the journaled P6 transaction boundary before enabling general mutation.
