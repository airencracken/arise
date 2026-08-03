# Arise

Arise is an experimental, high-performance package manager for Gentoo Linux.
It combines package search, dependency resolution, repository synchronization,
installed-package inspection, source builds, configuration updates, and
recovery tools in one statically linked Go binary.

Arise reads the Gentoo configuration you already have in `/etc/portage` and
uses familiar package atoms and workflows. Its goal is to make everyday
package management fast while remaining useful when Python, Portage, or other
parts of a system need repair.

> Arise can perform live package transactions, but it is not yet a complete
> replacement for Portage. Releases remain testing-keyworded for `~amd64`.
> Keep Portage installed as the behavioral reference and recovery fallback.
> The current boundaries are documented in the
> [compatibility matrix](docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md).

## Performance

Arise keeps an indexed view of repository metadata and resolves dependency
graphs in one process. The comparisons below use equivalent results from the
same repository and installed-package snapshots; a faster result does not
count when the selected packages differ.

| Workload | Arise | Compared with | Other tool | Arise speedup |
|---|---:|---|---:|---:|
| List all installed package versions | 8.54 ms | `eix-installed` | 38.22 ms | **4.48x** |
| Search package names for Firefox | 14.60 ms | `eix` | 35.05 ms | **2.40x** |
| Search package names for Firefox | 13.66 ms | `emerge` | 960.59 ms | **70.34x** |
| Plan a shallow `@system` update | 1.96 s | `emerge` | 3.78 s | **1.93x** |
| Plan a deep/newuse `@world` update | 3.25 s | `emerge` | 17.98 s | **5.54x** |
| Rebuild the configured-repository index | 7.14 s | `eix-update` | 10.55 s | **1.48x** |
| Refresh an unchanged repository index | 3.79 s | `eix-update` | 10.52 s | **2.78x** |

These measurements were refreshed for the 0.0.7 release candidate on
2026-07-29. They come from one x86-64 Gentoo workstation and are not universal
performance guarantees. Commands, methodology, correctness checks, and claim
boundaries are in the
[performance results](docs/performance-results.md).

## Install from the overlay

The maintained
[Arise overlay](https://github.com/airencracken/arise-overlay) is the
recommended installation method on Gentoo:

```sh
eselect repository add arise-overlay git \
  https://github.com/airencracken/arise-overlay.git
emaint sync -r arise-overlay
```

Accept the testing keyword for Arise:

```text
# /etc/portage/package.accept_keywords/arise
sys-apps/arise ~amd64
```

Then install it with Portage:

```sh
emerge --ask sys-apps/arise
```

Once Arise is installed, it can synchronize configured repositories and
update itself:

```sh
arise sync
arise -1 --reinstall =sys-apps/arise-0.0.22
```

Git repositories synchronize concurrently. Arise honors Portage-compatible
`clone-depth` and `sync-depth` values from each `repos.conf` section. The
default is `1`; set either value to `0` to request full history.

Release ebuilds build without network access. New releases use a small,
deterministic vendor archive whose checksum is recorded in the overlay
Manifest. The archive carries a machine-verifiable provenance manifest and is
tested with empty Go caches and `GOPROXY=off`.

## Build from source

Arise requires Go 1.26.3 or newer:

```sh
git clone https://github.com/airencracken/arise.git
cd arise
make build
```

To build the static recovery-oriented binary used by the overlay:

```sh
make static
```

Run the standard validation suite with:

```sh
make test
make vet
```

See the [development guide](docs/development.md) for test lanes, environment
variables, architecture, and repository conventions.

## Quick start

Synchronize and search:

```sh
arise sync
arise search firefox
arise search --installed --category dev-lang
arise query --versions www-client/firefox
```

Inspect the installed system:

```sh
arise installed
arise installed --owner /usr/bin/gcc
arise installed --contents sys-devel/gcc
arise installed --uses sys-devel/gcc
arise installed --check sys-devel/gcc
arise query --ebuild sys-devel/gcc
arise inspect sys-devel/gcc
arise inspect --json dev-python/sphinx
arise info
```

`inspect` combines installed and available versions, visibility and effective
USE provenance, dependencies and reverse dependencies, kernel requirements,
out-of-tree module state, and incomplete-evidence diagnostics from one
stabilized snapshot. Its versioned JSON report is suitable for Maize, future
TUIs, and other tools that work alongside Arise.

Arise is a self-contained alternative with capabilities that overlap
`emerge`, `eix`, `equery`, `portageq`, and the `q` tools. It does not replace
them or clone their command names. If diagnosing package state or a failed
Arise operation requires one of those tools, that is uncovered Arise surface
and should be reported as a bug.

Preview changes before allowing a live transaction:

```sh
arise --pretend install app-editors/vim
arise --pretend --verbose update
arise --pretend --complete-graph --deep update
```

Review protected configuration files and repository news:

```sh
arise dispatch-conf
arise news display
```

Create a local, reviewable diagnostic report without uploading anything:

```sh
arise bug-report --output arise-bug-report
```

See the [bug-report guide](docs/bug-report.md) for the collection and redaction
boundary.

Use `arise help`, `man arise`, or the
[documentation index](docs/README.md) for the complete command surface.

## Safety and correctness

Arise validates a proposed package plan against an independently constructed
final state before execution. Live operations use Manifest-verified sources,
isolated build phases, collision checks, package journals, operation locking,
and serialized filesystem/VDB commits. Package preparation can run in
parallel, and interrupted transactions expose explicit recovery operations.

Unsupported behavior fails visibly. Compatibility is tested against Portage
and Gentoo fixtures, while Arise's final-state validator remains independent of
Portage's preferred plan or action ordering.

Useful references:

- [Documentation index](docs/README.md)
- [Portage compatibility contract](COMPATIBILITY.md)
- [Compatibility matrix](docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
- [Performance results and evidence](docs/performance-results.md)
- [Development guide](docs/development.md)
- [Release notes](docs/releases/)
- [Current engineering punch list](PUNCHLIST.md)

## Foundations and acknowledgements

Portage is Arise's behavioral reference and the source of much of its
package-management semantics. Gentoo, eix, Gentoolkit, portage-utils,
pkgcore/pkgcheck, and the wider Gentoo ecosystem provide essential prior art
and comparison points.

Arise is a small experimental project maintained by one developer with AI
assistance. Compatibility and performance claims are backed by tests and
retained evidence wherever possible.

## License

GPL-3.0. See [LICENSE](LICENSE).
