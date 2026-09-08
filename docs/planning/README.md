# Active planning index

Plans decompose future work beneath `../../PUNCHLIST.md`. They describe intended
design and acceptance gates, not necessarily current implementation. Remove completed plans after preserving any remaining requirements and repairing
links; Git retains their history.

- [`BUILD_TIME_ESTIMATION_PLAN.md`](BUILD_TIME_ESTIMATION_PLAN.md) — explainable
  history, live remaining time, and parallel transaction makespan.
- [`COLOR_CONFIGURATION_PLAN.md`](COLOR_CONFIGURATION_PLAN.md) — semantic color
  roles, Portage compatibility, themes, and accessibility.
- [`EXECUTION_RECOVERY_PLAN.md`](EXECUTION_RECOVERY_PLAN.md) — keep-going,
  bounded retry, continuation, and re-resolution.
- [`DIAGNOSTIC_INTELLIGENCE_PLAN.md`](DIAGNOSTIC_INTELLIGENCE_PLAN.md) — bounded
  resolver traces, saved-plan differences, and read-only configuration diagnosis.
- [`FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md`](FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md)
  — Btrfs, OpenZFS and LVM whole-operation recovery, OverlayFS boundaries,
  retention and boot-safe provider promotion.
- [`JOURNAL_RECOVERY_UX_PLAN.md`](JOURNAL_RECOVERY_UX_PLAN.md) — actionable
  recovery status, inspection, retention, and corruption handling.
- [`LIFECYCLE_TRANSACTION_PLAN.md`](LIFECYCLE_TRANSACTION_PLAN.md) — optional
  pre-commit lifecycle mutation capture.
- [`OVERLAY_LISTING_READINESS.md`](OVERLAY_LISTING_READINESS.md) — audited
  prerequisites and a prewritten record for eventual Gentoo repository-list
  submission.
- [`PACKAGE_OUTPUT_UX_PLAN.md`](PACKAGE_OUTPUT_UX_PLAN.md) — Portage-compatible
  plan records and Arise runtime progress.
- [`PERFORMANCE_IMPROVEMENT_PLAN.md`](PERFORMANCE_IMPROVEMENT_PLAN.md) —
  correctness-gated profiling and optimization program.
- [`PHASE_QUERY_PREFLIGHT_PLAN.md`](PHASE_QUERY_PREFLIGHT_PLAN.md) — static
  query coverage plus the constrained ROOT/BROOT runtime fallback.
- [`SOLVER_LIBRARY_PLAN.md`](SOLVER_LIBRARY_PLAN.md) — reusable pure-Go solver
  boundary and migration.

## Relationship to the active backlog

Presentation, color, estimates, and journal UX feed N09; execution recovery
feeds S02–S04; lifecycle and snapshot strengthening feed N03/N04; performance
feeds S07/N08; phase queries feed N01/N03; diagnostics feed S06/L03; library
extraction feeds L01. Overlay-listing readiness remains a separately authorized
publication task; its dated audit must be refreshed before submission.

Design requirements in these plans remain open until their acceptance evidence
is recorded. Dated implementation notes explain the starting point and must not
be copied back into the punchlist as completed tasks.
