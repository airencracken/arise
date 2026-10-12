# Design constraints

The [punchlist](../../PUNCHLIST.md) owns priority and completion criteria.
These short notes retain design decisions for unfinished work. Proposed
commands and providers are not implemented support claims.

| Work | Constraints |
|---|---|
| Estimates | [History, uncertainty, parallel ETA and opt-in reporting](BUILD_TIME_ESTIMATION_PLAN.md) |
| Output | [Plan/progress grammar](PACKAGE_OUTPUT_UX_PLAN.md), [color](COLOR_CONFIGURATION_PLAN.md), [recovery status](JOURNAL_RECOVERY_UX_PLAN.md) |
| Continuation | [Approval, resume and bounded retries](EXECUTION_RECOVERY_PLAN.md) |
| Recovery | [Filesystem snapshots](FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md), [lifecycle capture](LIFECYCLE_TRANSACTION_PLAN.md) |
| Diagnostics | [Read-only evidence and separately approved writes](DIAGNOSTIC_INTELLIGENCE_PLAN.md) |
| Phase queries | [State-bound static/runtime queries](PHASE_QUERY_PREFLIGHT_PLAN.md) |
| Performance | [Equivalent inputs, profiles and budgets](PERFORMANCE_IMPROVEMENT_PLAN.md) |
| Libraries | [Pure-Go solver boundary](SOLVER_LIBRARY_PLAN.md) |
| Gentoo listing | [Separately authorized listing request](OVERLAY_LISTING_READINESS.md) |

## Deferred fresh-stage3 harness

Prefer a disposable QEMU/KVM guest over the single-UID namespace used by current
smoke tests. Preserve normal package/build-user ownership and isolation features.
Pin the signed stage3 digest, repository/profile/configuration, distfiles and
candidate binary. Keep an immutable base disk and discard each test's writable
copy after exporting evidence.

Start with a small source/binary install–upgrade–unmerge fixture. Then test a real
world update, cancellation, hard process death and resume in separate restored
guests. Compare payload hashes, CONTENTS, VDB, world, journals and dependency/
linkage results; finally boot and check services. A snapshot reset restores a
fixture, not proof that Arise recovered correctly. Retain serial logs, exact
commands, before/after state and all failed attempts. No VM or fresh-stage3 run
is required for the 0.0.42 publication decision.
