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

Lifecycle hooks run against a copy-on-write view of ROOT. Arise validates the
resulting delta, captures every destination preimage in its journal, and then
applies the delta. Candidate providers include kernel overlayfs and
fuse-overlayfs. Provider availability is never assumed from the filesystem
type alone; a disposable mount/write/remove probe is required.

### Filesystem snapshot

The whole mutation window is protected by a snapshot provider such as a Btrfs
subvolume snapshot or an LVM snapshot. Selection requires proof that ROOT is a
suitable snapshot boundary, sufficient snapshot capacity exists, rollback is
possible for the mounted root, and boot/recovery instructions are durable.
Snapshots do not replace the package journal: the journal remains the normal
inspection and recovery interface, while the snapshot is the final escape
hatch for writes outside its known set.

### Portage-compatible baseline

Arise commits the journaled payload and VDB first and then runs write-capable
`pkg_postinst`/`pkg_postrm` hooks. Hook failure is reported as a post-commit
failure and preserves logs/build state; it does not claim that the installed
package was rolled back. This is the baseline compatibility contract because
it matches Portage's broad transaction ordering. Arise's native journal still
protects mutations that Arise controls directly.

## Selection contract

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
