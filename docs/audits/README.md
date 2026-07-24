# Audit index

Audits are dated observations, not live status documents. Open findings must be
tracked in `../../PUNCHLIST.md`, an active plan, or a regression test. Preserve
machine-readable companions when they are needed to reproduce a finding.

## Current reference audits

- [`DOCUMENTATION_AUDIT_2026-07-24.md`](DOCUMENTATION_AUDIT_2026-07-24.md)
  classifies maintained, archived, wiki-bound, and evidence documentation for
  the clean-world checkpoint sweep.
- [`GENTOO_REPOSITORY_COMPATIBILITY_AUDIT_2026-07-24.md`](GENTOO_REPOSITORY_COMPATIBILITY_AUDIT_2026-07-24.md)
  classifies the complete current Gentoo repository against the phase worker;
  its machine-readable companion is
  [`gentoo-repository-compatibility-2026-07-24.json`](gentoo-repository-compatibility-2026-07-24.json).
- [`PACKAGE_ENVIRONMENT_PARITY_AUDIT_2026-07-23.md`](PACKAGE_ENVIRONMENT_PARITY_AUDIT_2026-07-23.md)
  records the LLVM-target environment incident and resulting configuration
  contract.
- [`PORTAGE_PHASE_EDGE_PARITY_AUDIT_2026-07-22.md`](PORTAGE_PHASE_EDGE_PARITY_AUDIT_2026-07-22.md)
  covers fetch denial, hook ordering, cleanup, logging, and related phase
  boundaries.
- [`PORTAGE_EXECUTION_PARITY_AUDIT_2026-07-21.md`](PORTAGE_EXECUTION_PARITY_AUDIT_2026-07-21.md)
  records the live-execution failures that established the current parity
  gates. Read later implementation notes and tests before treating any finding
  as still open.

## Historical checkpoints and reviews

- [`CHECKPOINT_P3_P6_2026-07-18.md`](CHECKPOINT_P3_P6_2026-07-18.md) and
  [`CHECKPOINT_2026-07-18.md`](CHECKPOINT_2026-07-18.md) are superseded
  development snapshots.
- [`AUDIT_2026-07-16.md`](AUDIT_2026-07-16.md) and
  [`PUNCHLIST_AUDIT_2026-07-17.md`](PUNCHLIST_AUDIT_2026-07-17.md) preserve
  early capability and task-ledger findings.
- `AUDIT_ARCHITECTURE.md`, `AUDIT_COVERAGE.md`, `AUDIT_CRITICAL.md`,
  `AUDIT_ERRCHECK.md`, `AUDIT_OVERVIEW.md`, and `AUDIT_TESTS.md` are the
  historical June codebase-review set.
- [`GENTOOPM_REFERENCE_AUDIT.md`](GENTOOPM_REFERENCE_AUDIT.md) is a dated
  prior-art review, not a dependency or current compatibility claim.
