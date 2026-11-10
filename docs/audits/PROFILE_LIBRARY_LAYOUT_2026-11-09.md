# Profile library layout audit

## Incident and recovery

The reported libxcrypt 4.5.2-r1 configure failure was a missing 32-bit link
input, not an omitted `-m32`: GCC invoked the linker with the correct compiler
mode but could not locate `Scrt1.o` or `crti.o`.

The host profile reported `LIBDIR_x86=lib` and `SYMLINK_LIB=no`. Both startup
objects existed in `/usr/lib32`, and glibc 2.43-r4's installed `CONTENTS` recorded
that location. Its `multilib` USE flag was enabled. This matches Arise's
incorrect directory selection: glibc's configure and install phases use
`get_libdir`, which Arise hardcoded to `lib32` whenever `ABI=x86`.

Restore glibc with Portage before using a version of Arise containing this fix:

```sh
emerge --oneshot =sys-libs/glibc-2.43-r4
x86_64-pc-linux-gnu-gcc -m32 -print-file-name=Scrt1.o
x86_64-pc-linux-gnu-gcc -m32 -print-file-name=crti.o
```

The compiler queries must return existing paths rather than bare filenames.
Then verify an actual 32-bit link or rebuild the failing package with Portage.
Do not repair this by moving whole library directories or adding directory
symlinks: the package database must describe the restored layout too.

The user's `revdep-rebuild` result did not exclude this failure: it checks
dynamic linkage, not missing compiler startup objects. Its orphaned Irssi Perl
modules are a separate finding.

## Corrected execution contract

Portage's `get_libdir` reads `LIBDIR_${ABI}` and falls back to `lib` when the ABI
or its directory is absent. It does not infer a layout from an architecture
name or `DEFAULT_ABI`. The worker now follows that contract for all supported
ABIs, including nested profile directories.

The same helper supplies `econf`'s default library directory and `dolib.a`,
`dolib.so`, `newlib.a` and `newlib.so`. Correcting it covers these callers and
ebuilds such as glibc that explicitly construct configure and install paths.

The configuration-to-worker allowlist already carried `LIBDIR_*` but discarded
`SYMLINK_LIB`, `KERNEL_ABI` and `FAKE_TARGETS_*`. These now survive transport.
In particular, glibc calls `multilib_env`, which uses `SYMLINK_LIB` to regenerate
its ABI directories. Retaining `LIBDIR_x86` alone would leave the alternate
`SYMLINK_LIB=yes` layout broken.

Extending the complete helper-image comparison to EAPI 9 also exposed EAPI 8
rules guarded by equality with `8`: fixed config/header installation options
and removal of empty image directories were not applied to EAPI 9. Those
conditions now include both supported versions. The comparison retains the
permission and complete image-tree checks; it does not ignore these failures.
The EAPI 9 fixture uses `doins` for translations because `domo` is banned there.
That path also exposed destination-selection behavior: `into`, `insinto` and
`exeinto` must create absent destinations with normal directory permissions,
and selecting or installing into an existing directory must preserve its mode.
The worker now creates those destinations and avoids applying `diropts` again
to existing `doins`/`newins` destinations. A dedicated regression checks both
the immediate directory creation and mode preservation.

## Existing artifacts and binary packages

The execution ABI advances from 6 to 7. Already committed Arise artifacts can
have wrong paths despite unchanged versions and USE flags, so the resolver
must plan repairs for older marked artifacts. Unmarked Portage packages retain
their normal policy. This is deliberately conservative: the ABI marker does
not identify which individual packages exercised the wrong helper.

Binary compatibility checks previously ignored this marker. XPAK captures and
Packages indexes could also discard it. The marker now survives XPAK/GPKG
capture, archive inspection, index generation and advertised metadata.
Selection rejects incompatible Arise binaries, and preflight and installation
check the actual archive again before filesystem mutation.

Older XPAKs that omitted the top-level marker can still carry it in their full
VDB recovery snapshot. Archive inspection reads that validated evidence so an
old capture cannot masquerade as an unmarked package. Conflicting top-level
and captured markers are rejected. Reading captured execution provenance is
separate from checking recovery identity: normal Portage metadata rewrites
remain readable, while recovery restoration retains its identity validation.

## Verification and limits

Before/after regressions cover the user's x86 layout, alternate `lib32`, x32,
MIPS n32, nested RISC-V directories, custom paths and missing ABI inputs. They
observe configure arguments, installed library locations and contents. Other
regressions cover configuration transport, old ABI repair, binary metadata
round trips, remote selection, legacy captures, conflicting evidence and
rejection before ROOT, work or journal creation.

The required live phase-contract lane compares complete helper images with
Portage for both x86 layouts, x32 and nested directories under EAPI 7/8/9. It
also runs the actual Gentoo multilib eclass through split workers for both
`SYMLINK_LIB` settings and links a real ELF32 consumer against a library
installed through the profile directory. That link fixture needs no installed
32-bit libc and does not claim runtime execution coverage.

An optional actual libxcrypt multilib build is available with
`ARISE_LIVE_LIBXCRYPT_BUILD=1` and the `live_portage` tag. It installs into a
disposable root and checks both ABI library locations. Its initial sandboxed
run linked the compiler probe but could not execute it: the tool sandbox sent
even a trivial ELF32 program `SIGSYS`. The same program ran successfully
outside that sandbox. Validation logs, including that failure, are retained in
`.validation/profile-library-layout/` in the primary checkout.
The actual libxcrypt build then passed outside the tool sandbox for both x86
and amd64, merging into a disposable root with both profile library locations
checked.

Host recovery and a full glibc rebuild/merge remain separate from disposable
fixture checks. This audit cannot enumerate every already affected host file
without that host's installed metadata and filesystem.
