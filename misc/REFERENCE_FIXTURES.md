# Reference fixture capture

The capture harness records read-only outputs from Portage, Gentoolkit,
portage-utils, eix, genlop, emlop and golop. Every case has separate command,
stdout, stderr and exit status files; nonzero query, permission or
conflicted-plan statuses are evidence and do not abort later cases. Emerge-log
readers may require the explicit privileged lane on systems whose log is not
world-readable.

The inventory also records Portage/Gentoolkit `e*` tools. Read-only capability
and query cases cover `euse`, `eshowkw`, `epkginfo`, `enalyze`, `ekeyword`,
`ebump`, `eselect`, and the installed eix companion scripts. Help/version output
is useful even when a damaged Python environment prevents substantive queries:
preserve that nonzero status and traceback as a recovery fixture rather than
retrying a mutating mode. Never invoke ekeyword/ebump edits, euse enable/disable,
eselect setters, eix cache/sync operations, or enalyze repair in this harness.

Smoke validation:

```sh
ARISE_CAPTURE_MODE=smoke support/fixtures/capture-reference-fixtures.sh /tmp/arise-reference-smoke
```

Standard capture:

```sh
support/fixtures/capture-reference-fixtures.sh /tmp/arise-reference-standard
```

Standard capture can take a few minutes on a large installed set. It prints one
progress line per case, shows a spinner plus elapsed seconds on an interactive
terminal, and limits each command to 120 seconds by default. Redirected and CI
output remains stable rather than emitting animation frames. Set
`ARISE_CAPTURE_CASE_TIMEOUT` to another positive number of seconds when a known
large read-only query needs more time; status 124 records a timed-out case and
does not abort the remaining capture.

Full capture adds expensive `@world` and `emerge --info` cases:

```sh
ARISE_CAPTURE_MODE=full support/fixtures/capture-reference-fixtures.sh /tmp/arise-reference-full
```

The optional privilege-boundary lane must be invoked explicitly. It remains
read-only and uses only queries and pretend plans:

```sh
sudo env ARISE_CAPTURE_MODE=privileged \
  /absolute/path/to/support/fixtures/capture-reference-fixtures.sh \
  /tmp/arise-reference-privileged
```

For P2 command-environment parity, capture two root bundles from the same shell
and repository snapshot. First accept normal sudo filtering:

```sh
sudo env ARISE_CAPTURE_MODE=privileged \
  /absolute/path/to/support/fixtures/capture-reference-fixtures.sh \
  /tmp/arise-reference-sudo-filtered
```

Then enable the harness's fixed, non-sensitive command-environment probe:

```sh
sudo env ARISE_CAPTURE_MODE=privileged ARISE_CAPTURE_ENV_PROBE=1 \
  /absolute/path/to/support/fixtures/capture-reference-fixtures.sh \
  /tmp/arise-reference-sudo-explicit
```

On systems without sudo, use the equivalent `su` commands:

```sh
su -c 'ARISE_CAPTURE_MODE=privileged /absolute/path/to/support/fixtures/capture-reference-fixtures.sh /tmp/arise-reference-su-filtered'
su -c 'ARISE_CAPTURE_MODE=privileged ARISE_CAPTURE_ENV_PROBE=1 /absolute/path/to/support/fixtures/capture-reference-fixtures.sh /tmp/arise-reference-su-explicit'
```

The probe applies documented fixed values only after privilege transition, so
it neither preserves the whole caller environment nor depends on `su`/sudo
preservation policy. Arise honors variables present in its process environment
but cannot and must not reconstruct removed values. Compare both root bundles
with the unprivileged standard bundle and sanitize all three before promotion.

The default lanes capture only `dispatch-conf` and `etc-update` capability
output (`--version`/`--help`). Do not run either tool's normal update workflow
against the live root for fixture collection: even inspection can alter
configuration-update session or archive state.

Behavioral configuration-update fixtures belong in a disposable `ROOT` with a
synthetic protected tree and isolated archive/config files. Cover recursive
`._cfgNNNN_*` discovery, `CONFIG_PROTECT` and `CONFIG_PROTECT_MASK`, identical
and auto-mergeable files, update/keep/skip/edit/merge/diff/quit decisions,
pre/post-session and pre/post-update hook ordering, archive/rollback behavior,
mode and ownership preservation, malformed candidates, symlinks, and
interruption at every filesystem mutation. For `etc-update`, also cover preen,
automodes `-3`, `-5`, `-7`, and `-9`, explicit scan paths, and
`PORTAGE_CONFIGROOT`/`EROOT`. Since `dispatch-conf` has no root-selection CLI,
run its behavioral cases in a chroot or otherwise mount-isolated root with an
isolated `/etc/dispatch-conf.conf`; never redirect it toward the host. Record
the initial and final tree, archive, command transcript, status, and tool
version for every case.

Set `ARISE_CAPTURE_TARGETS` to a whitespace-separated target corpus. Targets
are restricted to package/set-safe characters. Never add sync, clean, config,
merge, unmerge, depclean, fix or non-pretend operations to this harness.

Captured output is not automatically publishable. Before committing a bundle,
review and sanitize hostnames, repository paths, private overlay names, mirrors,
proxy settings and local package policy. Immutable repository/profile/VDB
fingerprints should be added during fixture promotion so outputs cannot be
compared across different package-state snapshots accidentally.

The resolver-state half of a promoted fixture is produced separately from the
reference-tool transcript:

```sh
arise state fixture > resolver-state.json
```

This schema includes the dependency expressions, installed USE/IUSE, EAPI,
slot/subslot, repository priority and visibility fields needed for an offline
resolver replay. It replaces repository filesystem paths with stable relative
identities and embeds a SHA-256 integrity fingerprint. Loading rejects unknown
fields, incompatible schemas and any content whose fingerprint no longer
matches. Repository names and package policy remain semantically significant;
review private overlay names and local policy before promotion.
