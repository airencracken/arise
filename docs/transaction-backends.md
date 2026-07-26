# Transaction rollback backends

Arise treats rollback strength as an approved plan property, not an incidental
executor detail. The selected backend and its verified capabilities must be in
the saved plan digest and must be revalidated under the operation lock before
the first mutation.

## Guarantee tiers

### Journal

The native durable journal captures every path Arise itself will mutate before
the mutation occurs. It covers image merge, VDB/world updates, replacement and
unmerge. It does not by itself capture arbitrary writes made by package
lifecycle programs.

### Captured lifecycle

This tier is an additional, USE-gated Arise enhancement. Its absence never
disables the Portage-compatible baseline.

Lifecycle hooks run against a copy-on-write view of ROOT. Arise validates the
resulting delta, captures every destination preimage in its journal, and then
applies the delta. Candidate providers include kernel overlayfs and
fuse-overlayfs. Provider availability is never assumed from the filesystem
type alone; a disposable mount/write/remove probe is required.

The dependency-free baseline may instead stop lifecycle filesystem syscalls
before mutation and durably capture each preimage in the same journal. This
preserves Portage's phase isolation semantics without requiring a copy-on-write
filesystem. Ptrace is the initial implementation path; seccomp user
notification may provide a runtime-selected selective-notification backend once
its multithreaded argument-race handling proves the identical guarantee. Their
fail-closed coverage and promotion tests are specified in
`planning/LIFECYCLE_TRANSACTION_PLAN.md`.

### Filesystem snapshot

The whole mutation window is protected by a snapshot provider such as Btrfs,
OpenZFS or LVM. Selection requires proof that ROOT is a suitable snapshot
boundary, every separate mount/dataset/subvolume/LV in the mutation scope is
accounted for, sufficient snapshot capacity exists, rollback is possible for
the mounted root, and boot/recovery instructions are durable.
Snapshots do not replace the package journal: the journal remains the normal
inspection and recovery interface, while the snapshot is the final escape
hatch for writes outside its known set. OverlayFS and fuse-overlayfs are
lifecycle-delta/rehearsal providers, not persistent post-commit snapshot
backends. The provider contracts, retention rules and promotion matrix are in
`planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md`.

Arise deliberately preserves the ordinary Gentoo ROOT and VDB model. It does
not implement rollback by replacing package paths with generation symlinks,
building an immutable package store, or retaining parallel ROOT generations.
Those designs require a second path ownership and garbage-collection model and
would weaken Portage interoperability.

### Pre-update recovery binpkgs

When Arise subsumes `quickpkg`, it may capture every installed package that an
approved transaction will replace or remove as a verified host-derived binary
package before mutation. The complete pre-update set provides a portable,
filesystem-independent downgrade path when snapshots are unavailable.

This is package-set reconstruction, not atomic system rollback. Recovery must
re-resolve the complete captured set against actual state, restore it through
normal journaled package transactions, and require new approval for any changed
action. Lifecycle side effects, package-unowned files, external databases and
configuration outside the captured package/VDB state remain outside the
guarantee.

Recovery artifacts must retain exact package/VDB identity, content and metadata
integrity, the originating ROOT/plan fingerprint, missing or locally modified
path warnings, and their recovery-set membership. Retention is bounded, but a
set referenced by an active operation, failed verification or pending rollback
cannot be pruned.

### Portage-compatible baseline

Arise commits the journaled payload and VDB first and then runs write-capable
`pkg_postinst`/`pkg_postrm` hooks. Hook failure is reported as a post-commit
failure and preserves logs/build state; it does not claim that the installed
package was rolled back. This is the baseline compatibility contract because
it matches Portage's broad transaction ordering. Arise's native journal still
protects mutations that Arise controls directly.

## Selection contract

Portage-compatible phase isolation and lifecycle ordering are the baseline,
independent of every
rollback provider in this document. Arise mirrors Portage's phase-specific
`sandbox`, `usersandbox`, network, IPC, PID, mount and privilege behavior even
when no experimental provider is available. Bubblewrap is optional hardening;
it must never be required to resolve, preflight or execute an ordinary live
transaction.

Likewise, Arise's static Go process does not protect an installed Python
interpreter merely because Portage would currently be running under it. Python
versions remain governed by the normal dependency, slot, USE, ABI and
whole-state validation rules. Arise adds no package-manager-survival retention
or ordering edge for the current interpreter or its owning package.

Additional rollback providers are experimental strengthening layers. Their
user-facing policy should distinguish at least:

- `--experimental-rollback=required`: require captured-lifecycle or filesystem-snapshot
  coverage for every action; fail before mutation otherwise.
- `--experimental-rollback=auto`: choose the strongest verified backend, but print and bind
  that exact choice into the plan.

When an experimental provider is selected, its identity, scope, probe evidence,
capacity requirements and fallback policy belong in the plan. Re-resolution or
backend drift invalidates approval. Failure to select an experimental provider
does not make the normal Portage-compatible lane unavailable.

Backend selection applies per transaction chunk. A self-hosting Portage/Arise
bootstrap, critical toolchain chunk and ordinary leaf-package chunk may select
different providers, but each is independently saved and approved.
