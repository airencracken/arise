# Filesystem snapshot rollback plan

## Objective

Provide an optional whole-operation recovery point around approved live package
transactions while preserving the normal Gentoo filesystem and Portage VDB
model. The package journal remains responsible for immediate pre-commit crash
recovery. A filesystem snapshot adds a separately verified way to return from a
committed multi-package operation whose runtime or reboot verification fails.

This is not an immutable package store. Arise will not replace ordinary Gentoo
paths with package-generation symlinks, retain parallel ROOT trees as its
primary package model, or introduce a second garbage collector for path
generations. Selective downgrade remains a package/binpkg operation; coherent
whole-operation rollback belongs to the filesystem provider.

## Required semantics

A provider is eligible only when it can prove all of the following before the
first mutation:

1. Every persistent path in the approved mutation scope belongs to an
   identified snapshot boundary. At minimum this includes ROOT, its Portage VDB
   and the world/configuration files Arise will update.
2. Separate filesystems, datasets, subvolumes and mount points are enumerated.
   An excluded child is reported explicitly; it is never assumed to be covered
   by a parent snapshot.
3. Snapshot creation, identification, capacity inspection, rollback and
   deletion are supported by the exact selected provider.
4. The rollback procedure works for the currently mounted root. If offline
   activation, an initramfs, a boot-environment switch or a reboot is required,
   those requirements are durable and verified before mutation.
5. Snapshot identity and scope can be bound into the approved plan and operation
   record, then revalidated under the operation lock.
6. The provider can detect exhaustion, invalidation and disappearance. Losing
   rollback coverage after mutation begins is a terminal safety event.

The provider record contains a versioned schema, provider kind, stable
filesystem/device identities, covered and excluded mount points, snapshot
identifiers, creation time, capacity evidence, activation method, boot
requirements, retention class and a digest. Human-readable command output is
diagnostic evidence, not the machine interface.

## Operation protocol

1. Resolve and approve the exact package plan and rollback policy.
2. Acquire the operation/VDB lock and recover any active package journal.
3. Revalidate mount topology, provider identity, free-space/capacity evidence
   and recovery prerequisites.
4. Flush Arise-owned durable state, create the provider snapshot set and verify
   that every snapshot is addressable.
5. Persist and sync the snapshot record before the first package mutation.
6. Execute each package through its existing payload/VDB journal. Package-local
   failures normally use journal recovery without reverting successful peers.
7. Record runtime, service, preserved-library and reboot verification against
   the completed operation.
8. Mark the snapshot successful and eligible for retention or pruning only
   after the configured verification gate passes.

Multi-boundary snapshots are a coordinated recovery set, not magically atomic.
Arise must record their creation order and, on partial creation, delete only the
snapshots it proved it created. A crash between members leaves a recoverable
incomplete set and must not authorize package mutation. Rollback order must be
provider-defined and must restore VDB/configuration and payload state to the
same operation boundary.

## Provider contracts

### Btrfs

- Resolve the mounted ROOT to its filesystem UUID and subvolume ID; do not infer
  coverage merely from a `btrfs` filesystem type.
- Enumerate nested subvolume mount boundaries. A snapshot of a parent subvolume
  does not recursively snapshot independent nested subvolumes.
- Prefer a read-only pre-operation snapshot. Record the exact subvolume path,
  UUIDs and parent relationship.
- Prove the recovery method. A mounted root normally requires booting a
  different subvolume, changing the default subvolume, or performing an offline
  replace/rename; snapshot creation alone is not rollback readiness.
- Treat separate `/boot`, EFI, VDB, configuration or mutable application
  subvolumes as separate coverage decisions.
- Test qgroup/ENOSPC behavior, metadata exhaustion, nested subvolumes, snapshot
  deletion, interrupted setup and rollback from a rescue environment.

### OpenZFS

- Resolve every covered mount to a stable pool/dataset identity and enumerate
  child datasets. A parent snapshot name does not prove that every mounted child
  was included.
- Use a coordinated recursive snapshot only after validating the exact dataset
  set. Record pool GUIDs, dataset names, snapshot GUIDs and boot-environment
  properties where applicable.
- Prefer clone-and-activate or boot-environment switching for root recovery.
  Destructive in-place rollback must not silently discard newer snapshots,
  clones or unrelated dataset history.
- Check pool health, writable state, available space/reservations, encryption
  key availability and the import/boot path before mutation.
- Treat helper commands as optional runtime integrations with parsed,
  version-gated output and bounded execution. Their absence cannot break the
  journaled Portage-compatible baseline.
- Test child datasets, delegated properties, encrypted datasets, degraded
  pools, clone dependencies, partial recursive coverage and boot selection.

### LVM

- Distinguish classic copy-on-write snapshots from thin-pool snapshots. Record
  VG/LV UUIDs, segment type, origin, allocation policy and exact snapshot LV.
- For classic snapshots, calculate and reserve an explicit COW budget, monitor
  data usage during the operation and treat overflow/invalidation as loss of
  rollback coverage. A nominally created snapshot is not sufficient.
- For thin snapshots, verify thin-pool data and metadata headroom and monitor
  both. Overprovisioning is evidence to reject, not a reason to guess.
- Enumerate every covered LV. ROOT, `/usr`, `/var`, VDB and configuration spread
  across different origins require a coordinated recovery set.
- Prove the offline merge/activation and reboot path for a mounted root,
  including required initramfs tools. Never begin `lvconvert --merge` as an
  incidental cleanup action.
- Sync filesystems before snapshot creation and preserve application-specific
  caveats: crash consistency does not make arbitrary external databases
  transactionally consistent.
- Test COW exhaustion, thin metadata exhaustion, partial LV sets, renamed LVs,
  interrupted creation, offline merge and failure to activate the recovery root.

### OverlayFS and fuse-overlayfs

Overlay providers are not persistent whole-operation snapshot backends. They
remain useful for disposable transaction rehearsal and for capturing lifecycle
filesystem deltas before applying them through the native journal.

- Validate lower, upper and work directory identity, same-filesystem
  requirements, mount options and required xattr/whiteout behavior with an
  actual disposable probe.
- Audit whiteouts, opaque directories, redirect and metacopy behavior, hardlinks,
  xattrs, capabilities and rename semantics before accepting a delta.
- Never describe deleting an upper directory as rollback after its changes have
  already been copied into the live ROOT.
- Never pivot the normal Gentoo ROOT into a permanent overlay or generation
  symlink hierarchy to manufacture rollback semantics.
- Keep kernel OverlayFS and fuse-overlayfs as separate provider identities; a
  successful probe of one says nothing about the other.

## Retention and space policy

Snapshots are operation-scoped recovery objects with explicit ownership. The
default policy must bound count, age and reserved free space while never
silently deleting:

- an active or recovery-incomplete operation;
- the last verified recovery point;
- a snapshot named by an approved continuation or pending reboot verification;
- a provider object whose identity cannot be proven.

Pruning is a separately journaled administrative action. Arise reports expected
reclaim only when the provider can supply it; copy-on-write referenced space is
not estimated as the apparent file size. Manual provider snapshots remain
foreign and are never deleted.

## Failure and recovery UX

Before approval, report the exact coverage map, exclusions, capacity evidence,
expected activation method and whether rollback is online, offline or
reboot-mediated. After a failed operation, the static recovery binary must be
able to inspect the durable record without requiring the original repository
index.

Recovery never runs automatically merely because verification failed. Arise
offers:

- package-journal recovery for an uncommitted package;
- continuation from the actual committed state;
- explicit activation of the recorded filesystem recovery point; or
- manual recovery instructions when the provider cannot be safely controlled.

The command must require confirmation of the exact snapshot-set digest, warn
about excluded mounts and state that external/non-filesystem side effects may
survive rollback.

## Test and promotion matrix

The provider interface requires:

- unit tests for topology, capability, capacity and provider-output parsers;
- schema and compatibility tests for durable snapshot records;
- property tests over mount graphs and multi-boundary coverage sets;
- adversarial tests for hostile names, stale identities, symlinked helper
  paths, malformed output, timeouts and partial provider success;
- atomicity tests at every boundary between lock, record publication, snapshot
  creation, first mutation, verification and pruning;
- disposable privileged integration tests using loop devices, namespaces and
  provider-native rollback;
- boot/recovery tests for every provider advertised as root-capable; and
- cross-checks proving Portage reads the restored VDB and filesystem state.

Provider support remains experimental until its entire applicable matrix
passes. “Command succeeded” and “snapshot exists” are never sufficient
promotion evidence.
