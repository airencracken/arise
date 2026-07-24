# Documentation archive

This directory is a temporary staging area for superseded planning notes and
development records that are no longer maintained as current instructions. It
is not intended to grow indefinitely or become the project's permanent
historical record.

An archived document may still explain why a design or test exists. Its open
tasks must already be represented in `PUNCHLIST.md`, an active planning
document, or automated tests before it is moved here. Dated evidence and audits
normally remain in their dedicated directories because their historical status
is already explicit.

Do not cite archived material as current behavior. Prefer the root `README.md`,
`PUNCHLIST.md`, maintained manuals, and `docs/README.md`.

## Exit policy

At checkpoint and release sweeps:

1. Move durable project history or design narrative to the project wiki.
2. Convert reproducible claims into tests or compact machine-readable fixtures.
3. Update inbound links to the wiki, maintained docs, tests, or replacement
   evidence.
4. Delete the repository archive copy once no release, recovery, or
   reproducibility gate depends on it.

Repository history remains available through Git; an old prose file does not
need to remain in the current tree merely to prove that it once existed.

## Contents

- `planning/` — superseded task breakdowns whose remaining work moved into the
  punch list or maintained topic plans.
- `notes/` — time-bound research inventories retained for provenance.
