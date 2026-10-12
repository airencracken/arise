# Recovery guarantees

| Mechanism | Current scope |
|---|---|
| Package journal | Captures Arise-owned payload/VDB mutation before commit; supports interrupted rollback |
| Recovery sets | Capture replaced/removed installed packages for bounded, journaled reconstruction; full multi-package certification remains open |
| Lifecycle capture | Proposed optional tracing or copy-on-write delta capture; does not yet guarantee arbitrary hook writes are reversible |
| Filesystem snapshots | Proposed Btrfs/OpenZFS/LVM whole-operation recovery, requiring provider-specific certification |

A package journal is not a system snapshot. Recovery sets do not atomically undo
external databases, package-unowned files or arbitrary lifecycle effects.
Restore must check package/content/root identity, local modifications,
dependency closure and approval against actual state. Required active recovery
objects cannot be pruned. See [recovery tests](../internal/recoveryset).

Future strengthening is described by the [lifecycle capture](planning/LIFECYCLE_TRANSACTION_PLAN.md)
and [filesystem snapshot](planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md)
contracts. Provider selection, experimental rollback flags and new guarantee
tiers are proposals, not available command-line interfaces.
