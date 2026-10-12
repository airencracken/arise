# Filesystem snapshot rollback

Proposed whole-operation recovery, distinct from implemented package journals
and recovery sets. This is not an immutable package store. Keep ordinary Gentoo
ROOT/VDB paths and use the package journal for immediate pre-commit recovery.
Future `quickpkg` compatibility should add pre-update recovery binpkgs as the
portable middle tier, without claiming atomic system restoration.

## Shared contract

Each provider is an independently USE-gated implementation. The static
journal/recovery baseline remains fully functional with every provider disabled.
Requesting an unavailable provider fails before mutation; weaker fallback needs
approval. External helpers must be bounded and version/capability checked.

Before mutation, prove exact covered/excluded filesystems, ROOT/VDB/world/config
coverage, capacity, snapshot identity and online/offline/reboot activation.
Bind the versioned provider record into approval; revalidate under the operation
lock, recover journals, create and verify every snapshot, then sync the record.
Partial multi-boundary creation must never authorize mutation. Record creation
order and clean up only proven owned objects. Monitor exhaustion/disappearance.

After the transaction, verify runtime, linkage, services and required reboot
before marking the recovery point eligible for retention. Rollback is explicit
and bound to the snapshot-set digest. Disclose excluded mounts and external
side effects; filesystem consistency does not ensure database consistency.

## Providers

### Btrfs

Record filesystem UUID, subvolume ID and parent identity. Enumerate independent
nested subvolumes and separate boot/EFI/VDB/config mounts; parent snapshots do
not imply recursive coverage. Prove rescue/boot-subvolume activation. Test
qgroups, metadata/ENOSPC exhaustion, interrupted creation, deletion and rollback.

### OpenZFS

Record pool/dataset/snapshot GUIDs and exact recursive coverage. Prefer verified
clone/boot-environment activation; never discard unrelated clones/history during
rollback. Check pool health, reservations, encrypted keys and boot/import path.
Test child/delegated/encrypted datasets, degraded pools and partial coverage.

### LVM

Distinguish classic COW and thin snapshots; record VG/LV UUIDs and every origin.
Reserve/monitor COW capacity or thin data/metadata headroom. Prove offline merge,
initramfs and reboot activation. Test exhaustion, renamed LVs, partial sets,
interrupted creation/merge and unavailable recovery roots.

### OverlayFS and fuse-overlayfs

Overlay providers are not persistent whole-operation snapshot backends. Use
separate kernel/FUSE identities for rehearsal or lifecycle-delta capture.
Probe lower/upper/work identity, whiteouts, opaque directories, xattrs,
capabilities, hardlinks, rename, redirect and metacopy behavior. Deleting upper
state cannot undo changes already copied into ROOT.

## Retention and promotion

Never prune active/incomplete operations, the last verified recovery point,
objects needed by pending approval/reboot, uncertain identities or foreign
snapshots. Bound count/age/space; pruning is a separately journaled action.
Inspect durable records with the static recovery binary and no repository index.

Require unit tests for topology, capability, capacity and provider-output
parsers; schema/property/adversarial tests for records, mount graphs, hostile
names and partial success; atomicity tests at every boundary between lock,
record publication, snapshot creation, mutation, verification and pruning.
Disposable privileged tests must exercise native rollback and root boot recovery,
then prove Portage reads matching payload/VDB state. Certify each provider
independently before advertising it.
