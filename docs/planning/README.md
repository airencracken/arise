# Active planning index

Plans decompose future work beneath `../../PUNCHLIST.md`. They describe intended
design and acceptance gates, not necessarily current implementation. Completed
plans should be reconciled into maintained documentation and tests, then moved
to `../archive/planning/` with a dated archival note.

- [`BUILD_TIME_ESTIMATION_PLAN.md`](BUILD_TIME_ESTIMATION_PLAN.md) — explainable
  history, live remaining time, and parallel transaction makespan.
- [`COLOR_CONFIGURATION_PLAN.md`](COLOR_CONFIGURATION_PLAN.md) — semantic color
  roles, Portage compatibility, themes, and accessibility.
- [`EXECUTION_RECOVERY_PLAN.md`](EXECUTION_RECOVERY_PLAN.md) — keep-going,
  bounded retry, continuation, and re-resolution.
- [`JOURNAL_RECOVERY_UX_PLAN.md`](JOURNAL_RECOVERY_UX_PLAN.md) — actionable
  recovery status, inspection, retention, and corruption handling.
- [`LIFECYCLE_TRANSACTION_PLAN.md`](LIFECYCLE_TRANSACTION_PLAN.md) — optional
  pre-commit lifecycle mutation capture.
- [`PACKAGE_OUTPUT_UX_PLAN.md`](PACKAGE_OUTPUT_UX_PLAN.md) — Portage-compatible
  plan records and Arise runtime progress.
- [`PERFORMANCE_IMPROVEMENT_PLAN.md`](PERFORMANCE_IMPROVEMENT_PLAN.md) —
  correctness-gated profiling and optimization program.
- [`PHASE_QUERY_PREFLIGHT_PLAN.md`](PHASE_QUERY_PREFLIGHT_PLAN.md) — static
  query coverage plus the constrained ROOT/BROOT runtime fallback.
- [`SOLVER_LIBRARY_PLAN.md`](SOLVER_LIBRARY_PLAN.md) — reusable pure-Go solver
  boundary and migration.
