# Audit evidence

The [shell-state contract audit](SHELL_STATE_CONTRACT_2026-11-05.md) follows the
Arise self-upgrade failure across package names, attributes, eclass scope,
snapshot migration, and the missing self-host release gate.

The [install QA isolation follow-up](INSTALL_QA_ISOLATION_2026-11-05.md) records
the QA shell boundary missed by the phase-state audit and the exact Bash
completion syntax-error regression.

The [phase-state audit](PHASE_STATE_AUDIT_2026-11-04.md) covers state lost between
workers, source directories, lifecycle snapshots, isolation bindings, and
Portage differential tests prompted by the binutils-libs failure.

The [world-update audit](WORLD_UPDATE_AUDIT_2026-10-11.md) records the blocker
loop, ABI/validation regressions and measured latency correction.
The [release review](../reviews/2026-10-11-stabilization-release.md) covers
resume, transactions, recovery and binary planning.

`gentoo-repository-compatibility-2026-07-24.json` retains the historical
repository classification input. Older prose audits are in Git history;
remaining requirements are in [open work](../../PUNCHLIST.md).
