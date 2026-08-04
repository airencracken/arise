# Unofficial Gentoo Handbook addendum: Arise

This unofficial addendum describes one conservative way to bring up an amd64
Gentoo stage3 with Arise as the day-to-day package manager. Follow the Gentoo
Handbook for disk layout, filesystems, stage3 verification and extraction,
mounts, DNS, chroot entry, kernel, init system, networking, bootloader, users,
and services. This document replaces only the repository and package-manager
handoff portion of that process.

Arise is experimental. Keep `sys-apps/portage` installed and keep a tested VPS
snapshot or rescue path. This procedure establishes the G1 claim that Arise
can maintain an existing stage3; it is not a stage1 bootstrap or an independent
stage3 construction.

## Reference choices

The reference installation deliberately chooses a small policy surface:

- amd64 stage3 matching the intended OpenRC or systemd profile;
- the profile already selected by that stage3 until its news says otherwise;
- HTTPS Git synchronization for every ebuild repository;
- the sync-friendly Gentoo Git mirror with shallow history;
- the Arise overlay over HTTPS Git with shallow history;
- stable Gentoo keywords, with only `sys-apps/arise` accepted on `~amd64`;
- the stage3's compiler flags, with no copied `-march` value;
- conservative build parallelism sized for the VPS memory and disk;
- Portage retained as the compatibility reference and recovery path;
- a frozen, reviewable Arise plan before the first live world transaction.

Git-only synchronization means `net-misc/rsync` is not required for repository
updates. It does not imply that arbitrary ebuilds can never depend on rsync.

## Before changing the stage3

Record enough information to reproduce the starting point:

```sh
sha256sum stage3-*.tar.xz
date -u
uname -m
```

Retain the published stage3 digest and signature verification result. Record
the VPS image or snapshot identifier, storage size, memory, CPU count, and the
network boundary. After entering the chroot, record the initial package state:

```sh
eselect profile show
emerge --info
qlist -ICv > /root/stage3-packages.txt
```

If `qlist` is unavailable, record `find /var/db/pkg -mindepth 2 -maxdepth 2`
instead. Do not install an extra inspection package merely to satisfy this
documentation step.

## Configure Git-only repositories

Install Git once with Portage if the chosen stage3 does not already contain it:

```sh
emerge --ask --oneshot dev-vcs/git
```

Create `/etc/portage/repos.conf/gentoo.conf`:

```ini
[DEFAULT]
main-repo = gentoo

[gentoo]
location = /var/db/repos/gentoo
sync-type = git
sync-uri = https://anongit.gentoo.org/git/repo/sync/gentoo.git
auto-sync = yes
clone-depth = 1
sync-depth = 1
```

The URI is Gentoo's sync-friendly Git repository, which includes the metadata
needed by package managers. Do not substitute a developer repository that
lacks the generated metadata cache. A full history is unnecessary for normal
package management; set both depth values to `0` only when deliberately doing
repository archaeology.

Synchronize the initial Gentoo repository with Portage:

```sh
emaint sync -r gentoo
```

Review the profile rather than selecting a profile by a copied list number:

```sh
eselect profile show
eselect profile list
```

Keep the profile family and version supplied by the stage3 unless a current
Gentoo news item explicitly requires migration. OpenRC versus systemd is a
stage3/profile decision, not an Arise decision.

## Keep host policy modest

Start from the stage3 `/etc/portage/make.conf`. Do not copy another machine's
`COMMON_FLAGS`, `CPU_FLAGS_X86`, `VIDEO_CARDS`, `USE`, or `MAKEOPTS`. In
particular, an `-march=native` binary built on the installation host may not run
on the VPS CPU after migration.

For a small VPS, begin with one package build at a time and modest compiler
parallelism. Increase concurrency only after measuring memory, load, disk space,
and build behavior. Arise also protects parallel execution with a free-space
guard under `PORTAGE_TMPDIR`; prefer reducing jobs over disabling that guard.

Set locale, timezone, mirrors, licenses, and package-specific USE policy using
the ordinary Handbook and Portage files. Arise reads those files rather than
maintaining a second configuration system.

## Add the Arise overlay

Install the repository registration helper with Portage:

```sh
emerge --ask --oneshot app-eselect/eselect-repository
mkdir -p /etc/portage/repos.conf
eselect repository add arise-overlay git \
  https://github.com/airencracken/arise-overlay.git
```

Confirm that `/etc/portage/repos.conf/arise-overlay.conf` uses Git and add
shallow-history settings if `eselect-repository` did not write them:

```ini
[arise-overlay]
location = /var/db/repos/arise-overlay
sync-type = git
sync-uri = https://github.com/airencracken/arise-overlay.git
auto-sync = yes
clone-depth = 1
sync-depth = 1
```

Then synchronize only that repository:

```sh
emaint sync -r arise-overlay
```

## Install the first Arise

The currently supported handoff builds the versioned package from the overlay:

```sh
mkdir -p /etc/portage/package.accept_keywords
printf '%s\n' 'sys-apps/arise ~amd64' \
  > /etc/portage/package.accept_keywords/arise
emerge --ask sys-apps/arise
```

Use a package-specific keyword file. Do not apply `ACCEPT_KEYWORDS="~amd64"`
to the whole bootstrap transaction.

The planned `sys-apps/arise-bin` package will avoid installing Go during this
handoff, but it is not a supported route until a release publishes an immutable
static binary, provenance metadata and checksums and the overlay contains the
matching ebuild. Copying an unpublished local binary is useful for development
or recovery, but does not satisfy this reproducible installation procedure.

Verify the installed control plane and preserve its identity:

```sh
arise --version
sha256sum /usr/bin/arise
git -C /var/db/repos/gentoo rev-parse HEAD
git -C /var/db/repos/arise-overlay rev-parse HEAD
eselect profile show
```

## Hand repository synchronization to Arise

Arise reads every configured `repos.conf` section. Confirm that no repository
still uses rsync, then synchronize and publish the resolver snapshot:

```sh
grep -R '^[[:space:]]*sync-type' /etc/portage/repos.conf
arise sync
```

Every reported repository must use `sync-type = git`, and the command must
finish repository checkout and resolver-index publication successfully. A
checkout that updates while index publication fails is not a successful sync.

At this point return to the Gentoo Handbook for the remaining base-system
installation. Use Portage for any package installation that must happen before
the Arise world gate if Arise exposes an unsupported operation; record that
exception rather than hiding it.

## First Arise world update

Complete the kernel, bootloader, networking, root password and essential
service configuration first. Take a restorable VPS snapshot immediately before
the first live Arise transaction.

Generate a complete deep/newuse plan with build dependencies included:

```sh
arise --pretend --verbose --update --deep --newuse --complete-graph \
  --with-bdeps=y --backtrack=20 --save-plan stage3-world update @world
sha256sum /var/tmp/arise/plans/stage3-world.json
```

Review and preserve the JSON plan. Repository commits, profile selection,
Portage configuration, world state, VDB state, or relevant resolver options
changing after review invalidates the authorization. Generate a new plan rather
than bypassing that protection.

Execute exactly the approved plan:

```sh
arise --ask --update --deep --newuse --complete-graph --with-bdeps=y \
  --backtrack=20 --approve-plan stage3-world update @world
```

Do not run depclean as part of this first transaction. First establish a clean,
bootable updated system and inspect any proposed removal separately.

## Interruption and recovery

On interruption, power loss, or package failure, inspect Arise's durable state
before starting a different package-manager transaction:

```sh
arise recover status
arise --resume update @world
```

Do not delete `/var/tmp/arise`, active journals, recovery sets, or the resume
file to force progress. Preserve a local diagnostic bundle before restoring a
snapshot or asking for help:

```sh
arise bug-report --output /root/arise-stage3-bug-report
```

The bundle is local and reviewable; inspect it before copying it off the VPS.

## Validate before reboot

Review protected configuration and news deliberately:

```sh
arise dispatch-conf
arise news list
```

Then run the non-mutating package-state gates:

```sh
arise recover status
arise maintain world --check
arise maintain moveinst --check
arise maintain merges --check
arise maintain resume --check
arise info --preserved-libs
arise --pretend --update --deep --newuse --complete-graph \
  --with-bdeps=y --backtrack=20 update @world
```

Treat active journals, preserved libraries, maintenance failures, ownership or
VDB inconsistencies, and a non-empty final plan as unresolved. Also test the
host's actual boot-critical behavior: dynamic linker, C and C++ compilation,
DNS and network access, remote login, init system, mounted filesystems, clock,
and any installed Python, Perl, Go, Rust, or LLVM workloads.

Preserve the reviewed plan, its digest, repository commits, `/etc/portage`,
`/var/log/emerge.log`, `/var/log/arise`, stage3 identity, and pre-transaction
snapshot identifier.

## Reboot gate

Reboot only after the pre-boot checks are clean and the rescue console or VPS
snapshot is available. After reboot, repeat:

- `arise recover status`;
- all four `arise maintain ... --check` commands;
- `arise info --preserved-libs`;
- the complete deep/newuse pretend world plan;
- network, remote-login, linker, compiler, runtime, mount, and service probes.

A clean result establishes the G1 maintenance gate for that recorded system.
An Arise-driven empty-tree rebuild is the separate G2 experiment and should not
be appended casually to the initial installation.
