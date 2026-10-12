# 0.0.42 verification

Behavior fixes were tested at `dc7d161509791e4fc4a68944e2cf288112f8a053`,
based on `ff908a5` plus the world-update fixes. The final release also removes
superseded documentation and refreshes its contracts. Artifact provenance binds
the release's final source revision; the earlier candidate remains private evidence.

## Regressions

| Area | Observable behavior and coverage |
|---|---|
| Resume | Accept legacy CPVs; preserve progress on pretend/declined ask; execute pending same-version rebuilds; retain original favorites without adding dependencies. [CLI](../../cmd/arise/install_planning_audit_test.go), [schema/progress](../../internal/resolve/resume_world_test.go) |
| Approval and empty updates | Honor ask/receipts before bookkeeping, preserve unrelated progress, bind resume/world intent. [Approval](../../cmd/arise/plan_authorization_test.go) and CLI cases |
| Replacement | Source and binary upgrade/reinstall remove predecessor VDB/obsolete payload in disposable roots; preserve build-only behavior. [Real workers, jobs 1/2](../../internal/executor/upgrade_execution_test.go) |
| Restore | Select current same-slot predecessor; preserve parallel slots; reject ambiguity/cancellation; retry and replay after injected failure. [Recovery](../../internal/recoveryset/upgrade_restore_test.go) |
| Remote binaries | One index per host, no pretend/declined-ask payloads; acquire the selected closure after approval. [HTTP/CLI execution](../../cmd/arise/binhost_plan_test.go) |
| Binary metadata/hooks | Preserve subslots, prefer compatible instances; source fallback or explicit rejection for required hooks. [Metadata](../../internal/binpkg/remote_plan_test.go), [hook boundary](../../internal/rebuild/binary_lifecycle_audit_test.go) |
| Comparison failures | Retain child diagnostics; failed/partial results cannot become accepted comparisons. [Command tests](../../cmd/arise-plan-compare/main_test.go) |

Remote-binary and hook regressions failed against exported pre-fix `efb57bb`.
Replacement, restore, instance/subslot and checkpoint failures were observed
before their fixes. The [world audit](../audits/WORLD_UPDATE_AUDIT_2026-10-11.md)
retains separate blocker, ABI, policy and independent validation evidence.

## Reference and artifact checks

Portage 3.0.81.2/Python 3.14.6 comparisons used frozen September repository,
profile/configuration, VDB and world copies, read-only to both tools with private
caches. Root-owned preserved-library registry, repository-revision and Portage
config-state files were unavailable in that capture; it is not a recovery snapshot.

| Workload | Verdict |
|---|---|
| Nano update | Equivalent valid, one action each |
| Nano deep/newuse | Valid divergence: 61/60 actions, optional extra nasm update |
| Coreutils build-only | Equivalent valid, one source action each |
| Nano onlydeps | Equivalent valid empty plans |
| Coreutils binary-only without cache | Both reject; failure evidence only |

Full Go tests, vet, internal/CLI races, live-tagged reference suites,
documentation checks and unchanged complexity ratchets passed on the behavior
candidate. Optional large-package rehearsals without configured inputs were
skipped. Packages-index fuzz smoke passed. Repeated vendor generation was
identical; provenance verification and the empty-cache offline Go suite passed.
The candidate source ebuild passed unpack, prepare, compile and disposable image
install. Published release validation additionally binds final source, vendor
and binary identities. Host package installation is a separate manual canary.

Five isolated paired warm runs after a warmup passed action parity for nano and
coreutils build-only; [performance results](../performance-results.md) record
medians. Failed namespace/reference attempts remain failed evidence.

## Deferred acceptance and limits

The signed-DIGESTS-verified OpenRC stage3 resolved nine updates and passed recipe/
policy preflight. Execution reached unpack but could not create normal build-user
ownership in the available single-UID/GID namespace; no package committed.
Full stage3 update, interruption/resume, linkage and reboot acceptance is
explicitly deferred for 0.0.42. A future [VM harness](../planning/README.md)
will preserve normal ownership and record before/after state.

Complete resume-option replay, binary lifecycle/dependency authority,
multi-package recovery with local/config changes, broader option/root cases and
cold/system performance budgets remain [open](../../PUNCHLIST.md). Reapply
original build/binary/root/job options on resume. Old checkpoints cannot recover
intent they never stored. Hook rejection/fallback is not hook execution support.
