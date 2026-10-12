# Stabilization release review, 2026-10-11

Candidate: Arise 0.0.42, prepared on `fix/stabilization-audit` from fetched
`origin/master` (`ff908a5`) plus the completed world-update audit (`ed1b222`,
`efb57bb`). The dirty main checkout, supplied logs, and unrelated worktrees were
preserved. No host package transaction or external publication was performed.

## Confirmed failures and regressions

| Area | Failure and resulting behavior | Coverage |
|---|---|---|
| Resume selection | Bare saved CPVs were rejected by the CLI atom parser. Planning changed `--skipfirst` checkpoints before confirmation; `--resume --update` could skip pending same-version rebuilds. Normalize legacy CPVs, skip in memory and force pending explicit resume targets. Publish the replacement checkpoint at execution. | [CLI planning and real resumed rebuilds](../../cmd/arise/install_planning_audit_test.go), [checkpoint planning](../../cmd/arise/confirmation_test.go) |
| Resume world state | Resumed dependencies became new favorites, and original favorites were lost after interruption. Store validated original selections in the same checkpoint; retain them through completion/skip and finish a pending world update when package progress is already complete. Legacy checkpoints cannot reconstruct absent favorites. | [schema, atomicity and progress](../../internal/resolve/resume_world_test.go), CLI decline/pretend/oneshot/completed-resume cases |
| Empty updates | An empty update ignored `--ask`, changed world and erased unrelated saved progress. Confirm before changing world and retire only the active resume operation. | CLI planning cases |
| Approval/bookkeeping | Empty updates bypassed a supplied approval. Saved world favorites were absent from authorization options. Apply the same approval/fingerprint checks before empty-update bookkeeping and bind resume semantics and sorted world selections into the receipt. A completed resume has no approvable package plan and rejects explicit package approval before mutation. | [canonical approval binding](../../cmd/arise/plan_authorization_test.go), CLI invalid-approval and completed-resume cases |
| Disposable-root upgrades | Executor replacement flags were limited to live ROOT. Source upgrades collided with the previous owner. Binary upgrades never selected a predecessor. Select replacements in both roots and remove the previous VDB/payload in the package transaction. Build-only retains its zero-install behavior. | [real source/binary install, upgrade and reinstall](../../internal/executor/upgrade_execution_test.go), jobs 1 and 2 |
| Recovery restore | Restore selected the captured version's path instead of the currently installed same-slot package and failed ownership preflight after an upgrade. Select the installed slot, preserve parallel slots and refuse ambiguity. | [upgrade, interruption, retry and replay](../../internal/recoveryset/upgrade_restore_test.go) |
| Recovery cancellation | A canceled restore changed set status and created work directories before checking its context. Reject initial cancellation before those effects. | Canceled restore and unchanged status/filesystem case |
| Remote binaries | Pretend/declined ask downloaded payloads. A source-fallback probe required unavailable source build tools even for a binary-only closure. Resolve from one remote index per host and acquire selected instances after confirmation/approval. | [HTTP fixture through real CLI and binary execution](../../cmd/arise/binhost_plan_test.go); dependency plus target; one index request, zero pretend/decline payloads |
| Binary ABI/instances | Generated `Packages` records lost subslots; compatible selection could choose an older build ID. Preserve ABI metadata and order compatible instances by numeric build ID and build time. | [metadata roundtrip and instance selection](../../internal/binpkg/remote_plan_test.go) |
| Binary lifecycle | Binary execution silently ignored required setup/install/removal hooks. Optional selection now falls back to source; strict/direct execution rejects declared or saved required hooks before ROOT mutation. Full hook execution remains unfinished. | [selection boundary](../../internal/resolve/binary_lifecycle_audit_test.go), [preflight, saved environment and old-removal cases](../../internal/rebuild/binary_lifecycle_audit_test.go) |
| Comparison evidence | Failed child commands appeared as JSON EOF, and partial/empty reference output could receive an accepted valid classification. Retain command diagnostics and require both commands to resolve before accepting or recording a successful comparison. | [comparison command and classification cases](../../cmd/arise-plan-compare/main_test.go) |

The remote-binary and binary-lifecycle CLI/library regressions were also run
against an exported `efb57bb` source tree and failed there. Source/binary upgrade,
restore ownership, binary subslot/instance and checkpoint mutation regressions
were observed failing before their corrections. The executor eligibility test
was updated to retain live-root authorization checks and cover build-only;
its old expectation that every disposable-root replacement flag be false was
incompatible with an actual upgrade.

## Frozen reference comparisons

Portage reference: 3.0.81.2, Python 3.14.6. Both tools consumed the existing
`frozen-host-20260908` repository, VDB, profile/configuration and world copies in
a read-only mount namespace, with private writable caches, temporary filesystems
and devices. The snapshot's completeness limits remain those recorded in the
[September review](2026-09-08-stabilization.md). Captures stayed private under
`.validation/stabilization-release-20261011/matrix`.

| Case | Result |
|---|---|
| Nano update | Equivalent valid plans, one action each |
| Nano deep/newuse update | Valid divergence, 61 Arise versus 60 Portage actions; the independent final-state assessment classifies the extra nasm 3.02 update as optional |
| Coreutils build-only reinstall | Equivalent valid plans, one source archive action each |
| Nano onlydeps | Equivalent valid empty plans |
| Coreutils binary-only with no usable cache | Both reject; preserved diagnostics identify the missing binary. This is failure-path evidence, not a successful plan or timing comparison |

Initial reference runs encountered namespace ownership, temporary-directory and
device access failures. Those runs are retained as failed attempts. The comparison
classification correction prevents them being counted as accepted comparisons.

## Fresh-stage3 attempt

A fresh unpack of `stage3-amd64-openrc-20260906T170102Z.tar.xz` used the frozen
Gentoo repository and the image's default amd64/23.0 profile. Its SHA-512 matched
the previously signed-DIGESTS-verified archive:

```
bd199be985f27edb140fe389db67efc7d25b45886a75619d42ecdc721cbb7a0617d385723da006140f5c86b4e4d87221677b8c8dfc9d317e21d912cc51ac2268
```

Archive ownership was normalized to the available mapped user, and device
nodes were supplied by the private namespace. The initial ordinary extraction
failed to create device nodes; a complete extraction excluding `./dev/*` passed.
The index contains 33,154 entries. Deep/newuse world resolution produced nine
actions, zero backtracks and no conflicts in about 1.36 seconds. Recipe/policy
preflight passed for all nine.

Execution first stopped during source acquisition. Supplying the two verified
cached unzip archives allowed `--resume` to reach `src_unpack`, where userpriv
workspace ownership failed. The available subordinate UID/GID entries are
`marcus:0:0`; a normal-host `unshare --map-auto --map-root-user` probe also failed
with `newuidmap: subuid overflow detected`. No package committed. The remaining
checkpoint and logs are preserved with the disposable image.

S05 still requires a complete update with normal ownership, deliberate
interruption/resume, final dependency/linkage verification and reboot evidence.

## Validation and candidate artifacts

Validation results and exact source/artifact checksums are retained outside Git
under `.validation/stabilization-release-20261011`. Final release checks are
recorded after the candidate source is committed. Source, static executable,
vendor archive and staged overlay files are prepared locally; external
publication remains pending.

The completed [world audit](../audits/WORLD_UPDATE_AUDIT_2026-10-11.md) measured
verbose Arise wall time at 5.29–5.33 seconds versus Portage 28.38–28.63 seconds
on that newer world snapshot. This review also captures separate same-snapshot
warm single-package and build-only timing runs with mandatory action parity.
Cold-cache, broader configuration and full system timing budgets remain S07 work.

## Remaining stabilization work

S01–S07 remain open: their acceptance criteria extend beyond this batch.
Priority follow-up is full binary lifecycle and binary dependency-authority
coverage, multi-package recovery with local/configuration changes, hard process
interruption through CLI resume, broad option/root combinations, and the full
fresh-stage3 gate. Existing real worker, merge, binary integrity, publication,
recovery, atomicity and live-reference suites support the tested subset. Optional
large-package rehearsals without their configured inputs remain unrun.

Remote metadata is planning evidence. Confirmed acquisition and package preflight
still have to validate the actual archives; an unavailable or changed approved
binary causes an execution failure rather than a silent replacement plan.
Older resume files cannot recover original selection/options that were never
recorded. Current checkpoints retain package progress and favorites; original
build-only, binary-only, root and job options still need to be supplied on resume.
Full option-context replay remains S02 work. Binary hooks are rejected/fallback boundaries, not completed lifecycle
support. These limits are reflected in the compatibility matrix and punchlist.
