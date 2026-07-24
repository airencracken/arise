# Documentation audit — 2026-07-24

## Scope

This audit covers root project documentation, user manuals, maintained guides,
planning documents, dated audits/evidence, and miscellaneous development notes.
It began while the final Firefox action of the first deep world-update
continuation was still running and continued through the defects exposed by
its post-run verification. The current post-sync update is a separate
19-action operation; no clean-world claim is made before that operation and
its verification complete.

## Authority model

The current tree should have a small, explicit documentation hierarchy:

1. `README.md`, `COMPATIBILITY.md`, and the installed manuals describe current
   user-visible behavior and safety boundaries.
2. `PUNCHLIST.md` is the live milestone/gate ledger until issue tracking
   replaces that role.
3. `docs/README.md` and directory indexes route readers by document type.
4. Active topic plans describe unimplemented design beneath the punch list.
5. Dated audits and evidence support historical claims but never override
   maintained docs or tests.
6. `docs/archive/` is a temporary migration queue, not permanent history.

## Drift corrected in this pass

- Replaced July 18 “latest checkpoint” links with the July 24 Portage
  self-hosting milestone and the authoritative punch list.
- Updated README, Texinfo, and man-page safety language: live install/update
  exists but requires the live-mutation safety gate and exact verified
  saved-plan approval.
- Documented saved plans, preflight-only validation, resume/work/plan paths,
  fetch concurrency, and advisory historical estimates in the manuals.
- Removed the nonexistent `--exclude` flag from the man page.
- Added omitted `select`, `state`, `installed`, and `bench` command coverage to
  the maintained manuals and synchronized Bash command/flag completion.
- Corrected `audit --fix`: pretend can list rebuilds, while non-pretend repair
  remains gated.
- Updated the README architecture map for the scheduler, journal, constrained
  installed queries, package-state authorization, lifecycle tracing, repository
  audit, snapshots, and VDB layers.
- Updated the Portage compatibility reference to 3.0.81.2 and corrected
  load-average and `PORTAGE_BINHOST` status using implementation/tests.
- Split implemented paths from the target `/etc/arise` and native
  state/cache/log layout.
- Corrected the release runbook's Make target description and replaced mutable
  tag deletion with immutable deprecation plus a patch release.
- Fixed `make install` documentation packaging so `arise.info` is generated
  from `arise.texi` before installation and treated as a generated artifact.
- Removed a stale performance-status paragraph from the maintained methodology;
  current measured claims remain in the README and benchmark matrix.
- Reconciled phase-query planning with the implemented static-preflight plus
  constrained ROOT/BROOT runtime-helper contract.
- Added audit, evidence, planning, and temporary-archive indexes.
- Documented that `arise sync` must publish a refreshed resolver snapshot
  before reporting success. A checkout-only sync produced a false empty world
  plan; the command now runs indexing as part of the operation.
- Replaced the dependency spinner with named, truthful resolver phases. The
  previous animation could keep moving during a multi-minute solve without
  identifying where time was spent.
- Recorded the empty-package VDB rule (`CONTENTS` is zero bytes, not a newline),
  historical NUL handling for `emerge.log`, installed-lifecycle environment
  isolation, and explicit built slot/subslot bindings.
- Added the planned `arise maintain world --check/--fix` counterpart to
  `emaint`, prompted by obsolete atoms still present in the host world file.

## Archived during this pass

- `docs/planning/P0_P4_REMAINING_TASKS.md` moved to
  `docs/archive/planning/P0_P4_REMAINING_TASKS_2026-07-17.md`; its actionable
  work is represented by the punch list, later audits, tests, and topic plans.
- `misc/REFERENCE_TOOL_CANDIDATES.md` moved to
  `docs/archive/notes/REFERENCE_TOOL_CANDIDATES_2026-07-17.md`; it is a
  repository-snapshot research inventory, not a maintained dependency list.

These files are candidates for wiki migration and deletion from the current
tree after inbound links and provenance are confirmed.

## Keep with the code

- Root README, compatibility contract, benchmark matrix, punch list, man page,
  Texinfo source, release/run/test guides, and configuration/transaction
  contracts.
- Active plans whose acceptance gates are still open.
- Small machine-readable fixtures used directly by tests or reproducible
  benchmark/parity workflows.
- The current milestone and any evidence required to establish the next
  checkpoint.

## Wiki migration candidates

- June codebase-review set (`AUDIT_ARCHITECTURE`, `AUDIT_COVERAGE`,
  `AUDIT_CRITICAL`, `AUDIT_ERRCHECK`, `AUDIT_OVERVIEW`, `AUDIT_TESTS`).
- July 16–18 capability audits and superseded checkpoints after the new
  checkpoint is published.
- Long narrative incident histories once their regression tests, punch-list
  gates, and compact evidence records are linked.
- Reference-tool and prior-art research that does not constrain a build,
  compatibility contract, or current test.

Git preserves removed prose history. Wiki migration should retain the original
date and source commit, then repository copies can be deleted rather than
duplicated indefinitely.

## Evidence compaction candidates

- Keep JSON evidence only while a maintained claim or reproducible comparison
  consumes it.
- Prefer minimized hermetic fixtures over full host snapshots.
- Move explanatory narrative to the wiki when the machine-readable record and
  test provide the durable repository artifact.
- Do not delete current recovery evidence, plan digests, binary digests, or
  transaction logs until the clean-world checkpoint and rollback/replay review
  are complete.

## World checkpoint status

- The v15 continuation committed all seven remaining package transactions,
  including Firefox 140.12.0 and the source-less `xorg-drivers` package.
- Its verifier correctly withheld a clean result. It exposed newline-only
  `CONTENTS` records for empty packages and already-stale built dependency
  bindings that Arise had not scheduled.
- The VDB records were backed up and repaired, the resulting rebuilds
  completed, and the `emerge.log` NUL region exposed by `golop` was backed up
  and removed. Regression coverage now guards both producers.
- A repository sync then introduced a new update. Arise v22 and Portage agree
  on its exact 19-package action set after correcting unexpanded installed
  `:=` dependencies; the frozen Arise binary passed recipe/policy preflight.
- Execution and post-run verification of that 19-action update remain pending.
  Accordingly, the G1 clean-world gate, experimental-switch removal, and
  deletion of older recovery evidence remain open.

## Validation performed

- `support/check-docs.sh`
- `git diff --check`
- targeted CLI/help-to-manual comparison
- repository-wide stale-status and inbound-link searches

The checkpoint reruns these checks after the world-result edits. The eventual
clean-world record must run them again if it changes maintained documentation.
