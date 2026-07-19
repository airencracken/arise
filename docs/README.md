# Arise documentation

Stable project entry points remain at the repository root:

- [`README.md`](../README.md) — project goals, status, usage, and measured claims.
- [`COMPATIBILITY.md`](../COMPATIBILITY.md) — compatibility and testing contract.
- [`BENCHMARK_MATRIX.md`](../BENCHMARK_MATRIX.md) — current and planned comparisons.
- [`PUNCHLIST.md`](../PUNCHLIST.md) — authoritative milestone and dependency graph.

The directories below contain development records. They are useful context, but
they may describe an older revision and do not override the root documents or
automated tests.

The latest consolidated state record is
[`audits/CHECKPOINT_P3_P6_2026-07-18.md`](audits/CHECKPOINT_P3_P6_2026-07-18.md).
Earlier dated checkpoints and audits are historical snapshots and must not be
read as current implementation status.

The maintained
[`compatibility/PORTAGE_COMPATIBILITY_MATRIX.md`](compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
tracks man-page-derived CLI options, environment variables, and Portage
configuration files together with their enforcement status.

## Audits

[`audits/`](audits/) contains dated and scoped codebase reviews. Audit findings
should migrate into tests, issues, or the punch list. Once every actionable
finding has migrated and the historical context is no longer useful, the audit
can be pruned.

## Evidence

[`evidence/`](evidence/) contains dated benchmark baselines, parity manifests,
coverage snapshots, and validation records. Prefer portable automated fixtures
over prose or host-specific evidence; prune a record after a newer reproducible
gate supersedes it.

## Planning

[`planning/`](planning/) contains working task breakdowns subordinate to the
root punch list. Reconcile completed or abandoned tasks into `PUNCHLIST.md`
before pruning a planning document.

## Maintained guides

Operational and implementation guides currently remain under [`misc/`](../misc/)
alongside their scripts and workload definitions. They can move into dedicated
guide/reference directories once their interfaces stabilize.

Run `./check-docs.sh` after documentation or CLI changes. It always checks Bash
syntax and whitespace, compiles `arise.texi` when `makeinfo` exists, and lints
`arise.1` when `mandoc` exists. Missing optional documentation tools are
reported and skipped rather than invoked blindly.

## Pruning checklist

Before deleting a development record:

1. Confirm every open finding is represented by a test, issue, or punch-list item.
2. Confirm no maintained document links to the record as authoritative evidence.
3. Preserve machine-readable fixtures needed to reproduce a compatibility claim.
4. Update this index and run the repository link/document validation checks.
