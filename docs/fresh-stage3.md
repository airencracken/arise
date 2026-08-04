# Fresh stage3 maintenance

For the complete installation sequence and reference Git-only repository
configuration, use the
[unofficial Gentoo Handbook addendum](handbook-addendum.md). This shorter
runbook remains the transaction checklist for the G1 acceptance run.

This runbook is for the G1 acceptance case: Arise maintains an existing Gentoo
stage3. It is not the later stage1-to-stage3 construction claim. Keep Portage
installed as the reference and recovery path.

Record the stage3 filename and published digest before changing the system.
Also record the selected profile, architecture, repository revisions, and the
contents of `/etc/portage` alongside the VPS snapshot identifier.

## Install Arise

The only intentional Portage bootstrap operations in this workflow are the
tools needed to register the overlay and the first Arise installation:

```sh
emerge --oneshot dev-vcs/git app-eselect/eselect-repository
eselect repository add arise-overlay git \
  https://github.com/airencracken/arise-overlay.git
emaint sync -r arise-overlay
mkdir -p /etc/portage/package.accept_keywords
printf '%s\n' 'sys-apps/arise ~amd64' \
  >> /etc/portage/package.accept_keywords/arise
emerge --ask sys-apps/arise
```

Do not remove Portage, Git, or `eselect-repository` after this handoff. The
versioned Arise ebuild builds offline after Portage fetches its source and
verified vendor archives, but the initial emerge still installs its declared
build and runtime dependency closure.

Capture the installed identity before using it:

```sh
arise --version
sha256sum /usr/bin/arise
git -C /var/db/repos/gentoo rev-parse HEAD
git -C /var/db/repos/arise-overlay rev-parse HEAD
eselect profile show
```

## Synchronize and freeze the plan

Take a VPS snapshot before the first live Arise transaction. Then synchronize
all configured repositories and publish the resolver index:

```sh
arise sync
arise --pretend --verbose --update --deep --newuse --complete-graph \
  --with-bdeps=y --backtrack=20 --save-plan stage3-world update @world
```

Review `/var/tmp/arise/plans/stage3-world.json`. Preserve that file and its
SHA-256 digest with the stage3 and repository evidence. A repository, profile,
Portage configuration, world, VDB, or relevant option change invalidates the
authorization; generate and review a new plan instead of overriding the
check.

Before approving mutation, verify that enough space remains under
`PORTAGE_TMPDIR` for the selected parallelism. Arise defaults to requiring 18
GiB free before starting parallel package jobs; use fewer jobs rather than
disabling the guard on a small VPS.

Execute exactly the reviewed plan:

```sh
arise --ask --update --deep --newuse --complete-graph --with-bdeps=y \
  --backtrack=20 --approve-plan stage3-world update @world
```

If the command is interrupted, inspect the durable state before doing anything
else:

```sh
arise recover status
arise --resume update @world
```

Do not delete `/var/tmp/arise`, active journals, or the resume file to force
progress. If resumption cannot proceed, preserve `arise recover status` and a
local diagnostic bundle before restoring the VPS snapshot:

```sh
arise bug-report --output /root/arise-stage3-bug-report
```

## Validate and reboot

After the transaction completes, deliberately review protected configuration
and repository news, then run the non-mutating state checks:

```sh
arise dispatch-conf
arise news list
arise recover status
arise maintain world --check
arise maintain moveinst --check
arise maintain merges --check
arise maintain resume --check
arise info --preserved-libs
arise --pretend --update --deep --newuse --complete-graph \
  --with-bdeps=y --backtrack=20 update @world
```

Treat pending journals, preserved libraries, maintenance failures, or a
non-empty final plan as unresolved. Also exercise the workloads the updated
host actually needs, including its C/C++ compiler, interpreter runtimes,
services, networking, and dynamic linker. Preserve `/var/log/emerge.log` and
`/var/log/arise` with the plan and configuration evidence.

Reboot only after those checks are clean. After reboot, repeat `arise recover
status`, the four maintenance checks, the preserved-library query, the empty
deep/newuse pretend plan, and the host-specific runtime probes. Passing this
workflow is evidence for G1 maintenance; an empty-tree rebuild is the separate
G2 gate.
