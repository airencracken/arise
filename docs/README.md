# Arise documentation

Stable project entry points remain at the repository root:

- [`README.md`](../README.md) — project goals, status, usage, and measured claims.
- [`COMPATIBILITY.md`](../COMPATIBILITY.md) — compatibility and testing contract.
- [`BENCHMARK_MATRIX.md`](../BENCHMARK_MATRIX.md) — current and planned comparisons.
- [`PUNCHLIST.md`](../PUNCHLIST.md) — authoritative milestone and dependency graph.

The directories below contain development records. They are useful context, but
they may describe an older revision and do not override the root documents or
automated tests.

The latest completed milestone record is the 2026-07-24
[`Portage self-hosting milestone`](evidence/PORTAGE_SELF_HOSTING_MILESTONE_2026-07-24.md).
The root punch list remains the authoritative live status until the current
world-update checkpoint is finalized. All dated checkpoints and audits are
historical snapshots and must not be read as current implementation status.

The maintained
[`compatibility/PORTAGE_COMPATIBILITY_MATRIX.md`](compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
tracks man-page-derived CLI options, environment variables, and Portage
configuration files together with their enforcement status.

Maintained reader guides extracted from the project overview:

- [`performance-results.md`](performance-results.md) — comparison tables,
  methodology, evidence, and claim boundaries.
- [`development.md`](development.md) — build/test commands, architecture,
  environment variables, and configuration boundaries.

## Audits

[`audits/`](audits/) contains dated and scoped codebase reviews; its
[`index`](audits/README.md) distinguishes current reference audits from
historical checkpoints. Audit findings
should migrate into tests, issues, or the punch list. Once every actionable
finding has migrated and the historical context is no longer useful, the audit
can be pruned.

## Evidence

[`evidence/`](evidence/) contains dated benchmark baselines, parity manifests,
coverage snapshots, and validation records. Prefer portable automated fixtures
over prose or host-specific evidence; prune a record after a newer reproducible
gate supersedes it. See the [`evidence index`](evidence/README.md) for scope and
supersession notes.

The 2026-07-24
[`Portage self-hosting milestone`](evidence/PORTAGE_SELF_HOSTING_MILESTONE_2026-07-24.md)
records Arise successfully installing `sys-apps/portage-3.0.81.2` and defines
the cumulative acceptance ladder from fresh-stage3 maintenance through a
repeatable stage1/bootstrap-to-stage3 construction.

## Planning

[`planning/`](planning/) contains working task breakdowns subordinate to the
root punch list. Reconcile completed or abandoned tasks into `PUNCHLIST.md`
before pruning a planning document. The
[`active planning index`](planning/README.md) summarizes each maintained plan.

[`planning/BUILD_TIME_ESTIMATION_PLAN.md`](planning/BUILD_TIME_ESTIMATION_PLAN.md)
defines explainable historical estimates, compatible sample selection, live
remaining-time updates, and dependency/job-aware transaction makespan.

[`planning/PERFORMANCE_IMPROVEMENT_PLAN.md`](planning/PERFORMANCE_IMPROVEMENT_PLAN.md)
defines the post-correctness profiling program, workload controls, suspected
hot paths, experiment protocol, and acceptance criteria for performance work.

[`planning/EXECUTION_RECOVERY_PLAN.md`](planning/EXECUTION_RECOVERY_PLAN.md)
defines commit-aware `--keep-going`, locked graph recalculation, continuation
approval, and bounded retry/rebatch semantics for long transactions.

[`planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md`](planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md)
defines provider-specific whole-operation rollback contracts for Btrfs,
OpenZFS and LVM, keeps OverlayFS scoped to rehearsal/lifecycle capture, and
rejects generation-symlink package stores.

[`planning/SOLVER_LIBRARY_PLAN.md`](planning/SOLVER_LIBRARY_PLAN.md) defines the
pure-Go reusable solver boundary, the Gentoo semantic frontend, first-class
explanations, and the ideas to study from libsolv without adding cgo or a
runtime dependency.

[`planning/COLOR_CONFIGURATION_PLAN.md`](planning/COLOR_CONFIGURATION_PLAN.md)
defines semantic color roles, Portage `color.map` compatibility, palette
explanations, accessibility themes and the no-color/static configuration
contract.

[`planning/PHASE_QUERY_PREFLIGHT_PLAN.md`](planning/PHASE_QUERY_PREFLIGHT_PLAN.md)
tracks complete `has_version`/`best_version` coverage across transitive eclass
closures and the representative Portage parity matrix.

## Archive

[`archive/`](archive/) contains superseded plans and notes whose actionable
work has moved into the punch list, tests, or maintained plans. Archived
documents retain their original context but are not current instructions. This
is a migration queue: durable narrative can move to the project wiki, while
repository copies should be deleted after links and reproducibility needs are
resolved.

## Maintained guides

Operational and implementation guides currently remain under [`misc/`](../misc/)
alongside their scripts and workload definitions. They can move into dedicated
guide/reference directories once their interfaces stabilize.

Run `./support/check-docs.sh` after documentation or CLI changes. It always checks Bash
syntax and whitespace, compiles `arise.texi` when `makeinfo` exists, and lints
`arise.1` when `mandoc` exists. Missing optional documentation tools are
reported and skipped rather than invoked blindly.

## Pruning checklist

Before deleting or archiving a development record:

1. Confirm every open finding is represented by a test, issue, or punch-list item.
2. Confirm no maintained document links to the record as authoritative evidence.
3. Preserve machine-readable fixtures needed to reproduce a compatibility claim.
4. Update this index and run the repository link/document validation checks.
