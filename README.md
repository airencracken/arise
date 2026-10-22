# Arise

Arise is an experimental package manager for Gentoo Linux. It combines search,
dependency resolution, repository sync, installed-package inspection, source
builds and recovery in a statically linked Go binary. It reads your existing
`/etc/portage` configuration and uses familiar package atoms.

Releases are testing-keyworded for `~amd64`. Keep Portage installed for reference
and recovery. See the [compatibility matrix](docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
for supported and incomplete workflows.

## Install

Add the [overlay](https://github.com/airencracken/arise-overlay):

```sh
eselect repository add arise-overlay git \
  https://github.com/airencracken/arise-overlay.git
emaint sync -r arise-overlay
```

Accept the testing keyword in `/etc/portage/package.accept_keywords/arise`:

```text
sys-apps/arise ~amd64
sys-apps/arise-bin ~amd64
```

Install the prebuilt static binary, or use `sys-apps/arise` for a source build:

```sh
emerge --ask sys-apps/arise-bin
```

The two packages are mutually exclusive. Source releases build offline from a
verified vendor archive; the optional `pie` USE flag selects a PIE build.

To update a source installation:

```sh
arise sync
arise -1 --reinstall =sys-apps/arise-0.0.43
```

## Use

```sh
arise sync
arise search firefox
arise query --versions www-client/firefox
arise installed --owner /usr/bin/gcc
arise inspect sys-devel/gcc
arise profile show --explain
arise profile list
arise --pretend install app-editors/vim
arise --pretend --verbose --update --deep --newuse @world
arise dispatch-conf
arise news display
arise recover status
```

`sync` and `index` evaluate uncached overlay metadata with an isolated Bash
worker. Run `arise index` after upgrading to refresh existing entries. Git sync
honors repository `clone-depth` and `sync-depth`; `0` requests full history.

Create a local diagnostic bundle with `arise bug-report --output arise-bug-report`.
The [bug-report guide](docs/bug-report.md) explains collection and redaction.
Use `arise help`, `man arise` or the [documentation index](docs/README.md) for
command details.

`arise profile show` displays inherited profile defaults and policy;
`--explain` includes their ordered source contributions and `--json` emits a
versioned report. `arise profile list` finds advertised profiles for the active
architecture; `arise profile diff REPOSITORY:PROFILE` shows what changes with
another profile. See [profile inspection](docs/profile-inspection.md) for scope
and examples.

## Correctness and performance

Arise validates a proposed package plan against an independently constructed
final state before execution. Transactions use verified sources, isolated build
phases, collision checks, durable journals and serialized filesystem/VDB
commits. Recovery guarantees and unsupported boundaries are described in the
[compatibility matrix](docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md).

The 0.0.42 audit fixed a world-update blocker loop that took over four minutes.
The same host plan then took about five seconds. Separate frozen, equivalent
single-package comparisons measured a 2.4–3.8x warm-cache lead over Portage.
These are workload-specific results; see [performance results](docs/performance-results.md)
for methodology and evidence.

## Build and contribute

Requires Linux and Go 1.26.3 or newer:

```sh
git clone https://github.com/airencracken/arise.git
cd arise
make build
make static
make test
make vet
```

See the [development guide](docs/development.md), [open work](PUNCHLIST.md) and
[0.0.43 release notes](docs/releases/0.0.43.md). Arise is maintained by one
developer with AI assistance. Portage, Gentoo, eix, Gentoolkit, portage-utils and
pkgcore/pkgcheck provide the reference behavior and comparison tools.

GPL-3.0. See [LICENSE](LICENSE).
