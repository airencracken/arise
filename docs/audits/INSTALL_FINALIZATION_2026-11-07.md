# Install finalization audit

The reported `gentoo-kernel-bin-6.18.60` failure exposed two gaps between the
phase worker and Portage's install finalization. The terminal error came from
stripping `certs/built-in.a`, a thin archive. Earlier `__vecho: command not
found` messages came from Portage's executable QA script.

## Stripping

Arise used successful `readelf -h` execution as permission to run
`strip --strip-unneeded`. `readelf` also accepts archives; it can follow thin
archives' references to objects outside the image. GNU strip cannot copy thin
archives. Using the same flags for regular archives also removed local symbols
that Portage preserves.

The worker now classifies file contents using `file`, following the object
classes handled by Portage's `estrip`:

- Executables, PIE executables, shared objects and relocatable ELF objects use
  the existing ELF stripping path.
- Regular archives receive debug-only stripping followed by `ranlib`.
- Thin archives and other formats remain unchanged. Archive members outside
  the image are never passed to strip.

Existing `dostrip -x` exclusions and `RESTRICT=strip` still apply. Errors from
stripping supported objects or rebuilding an archive's index still fail the
phase. This change does not add split-debug or cross-toolchain support.

## QA environment

Portage's `10executable-issues` script uses `__vecho` for spacing and diagnostics.
The worker provided the public logging functions but omitted this internal
helper. It now supplies `__quiet_mode` and `__vecho` within the QA subshell,
matching Portage's quiet-mode behavior and stderr destination. These functions
do not enter saved package state or later lifecycle hooks. Checks remain
isolated from one another, and advisory QA reports retain their existing
behavior.

## Verification

Before the fix, regression tests reproduced the thin-archive failure, loss of
regular-archive local symbols, and missing verbose helper in both quiet modes.
Afterward, tests check actual compiled executables, shared objects, relocatable
objects and archives, excluded files, external archive members, archive linking
and execution, and QA helper isolation through `pkg_preinst`.

The required live phase-contract lane now also compares finalization against a
real Portage `ebuild install` and exercises the warning branch of the installed
`10executable-issues` script with an executable-stack object. Regular archive
object payloads and symbols match Portage; archive timestamps are not compared.
Thin archives and excluded objects match byte for byte. Existing EAPI 7/8/9 and
Bash completion QA state checks remain in that lane.

These are disposable fixture images, not a complete kernel-bin merge or a live
host world update. Package-provided ELF QA diagnostics and process-restart
notices can still appear; neither is the thin-archive strip failure.
