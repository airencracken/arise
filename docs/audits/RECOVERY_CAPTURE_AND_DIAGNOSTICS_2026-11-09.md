# Recovery capture and execution diagnostics audit

## Incident and cause

A world update stopped at gzip while capturing an installed
`sys-kernel/gentoo-kernel-bin-6.18.62` rollback backup. The kernel's `CONTENTS`
listed `arch/x86/boot/initrd`, but the file no longer existed. Gentoo's
`kernel-install_pkg_config` in `kernel-install.eclass` deliberately removes
`initrd` and `uki.efi` placeholders when `generic-uki` is disabled. Its comment
also accounts for their prior removal by `emerge --config`.

Installed ownership records are not an assertion that every recorded path
still exists. Configuration dispatch and package hooks can legitimately remove
files. The previous special case for consumed protected-config updates did not
cover this broader class.

Arise captures the installed packages being replaced or removed across the
approved plan before its first filesystem mutation. That explains why the
kernel backup failure appeared while gzip was the current package. The raw
`executor: rebuild: enter mutation boundary: prepare mutation: recovery set`
chain obscured the actual operation, package and remedy.

## Capture and restore contract

Recovery artifact schema 2 records a missing payload path as `type: absent`
with its original `CONTENTS` type. The evidence is included in the payload
digest and embedded manifest; signed recovery sets also authenticate the
artifact digest. Missing entries have no invented file contents, permissions,
timestamps or link targets. The original installed database evidence is
retained, and capture does not edit the live `CONTENTS` file.

Only absence at the initial filesystem inspection is recorded this way.
Permission and I/O errors, malformed ownership records, incompatible existing
file types and symlink-parent escapes still fail. A file disappearing after
inspection remains a read failure. Absent paths are checked again during
archive writing and before publication; an appeared file, directory or dangling
symlink invalidates capture. These checks do not provide a filesystem snapshot
against a non-cooperating writer after the final check.

The payload archive contains existing files only. The new reader accepts schema
1 and 2; schema 1 cannot declare absent paths. An absent database record or a
present child beneath an absent parent is invalid. Older Arise readers reject
schema 2, so use the newer reader to inspect or restore newly created backups.
The recovery-set schema remains 1; it is independent of artifact evidence.

Restoration merges the captured image and regenerates installed ownership from
that image. Normal replacement cleanup removes newer owned paths absent from
the restored image. It does not fabricate removed placeholders or delete
arbitrary unrelated, unowned files. Standard GPKG capture also omits paths that
are already missing, retaining the normal GPKG format without Arise's recovery
absence evidence.

## Human-readable errors

Installation execution, removal-backup failures and `quickpkg` capture failures
use a shared explanation renderer. Typed errors carry the operation, affected
package or path, cause, next step and log location. The full technical wrapper
chain remains available with `--verbose`.

Covered explanations include rollback capture, build phases, saved-environment
reload failures, compiler link probes, storage exhaustion, file access,
operation locks, changed package plans, incompatible binary execution versions
and unsupported recovery formats. Filesystem reasons use error identities
rather than matching words in filenames. Shell output can supply a specific
compiler or storage hint; its original details and durable log remain visible.
Unknown errors retain their cause and request the evidence needed to report
them rather than inventing a repair command.

Post-install failures explicitly say that files and database changes are already
committed and direct the user to the failed follow-up step. Incomplete automatic
rollback directs the user to `arise recover status`. Neither condition is
presented as a routine retry. Concurrent failures retain each package's cause
and log. This is an execution-error contract, not a claim that every existing
CLI error has been converted.

## Verification

The missing-kernel-payload regression failed before the fix for both recovery
and GPKG capture. A separate real-ebuild configure failure caught a source-phase
path that still bypassed structured diagnostics; that regression also failed
before the producer was converted.

Tests cover missing files, directories and symlinks, consumed and pending
protected configuration, unchanged live ownership records, absent-path races,
schema validation and legacy artifact reading. An integration fixture publishes
a gzip/kernel backup set, upgrades both packages, and restores the set,
checking that the original kernel returns without recreating removed
placeholders. Existing atomic replacement and symlink safety checks remain.

CLI tests cover default and verbose output, durable logs, simultaneous package
failures, compiler and storage hints, committed post-install state, both causes
of failed rollback and concrete plan-recalculation commands. A real ebuild
failure verifies the producer-to-diagnostic-to-log path.

Validation includes `make test vet`, race checks for the changed packages,
`make test-live-phase-contract`, documentation checks and the complexity
ratchet. Logs are retained in `.validation/recovery-missing-payload/` in the
primary checkout. Disposable-root tests do not establish that the user's full
world update has completed; the new code must be installed before that retry.
