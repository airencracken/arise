# Arise — Gentoo Package Manager

A high-performance, self-healing package manager for Gentoo Linux, written in
Go. Statically linked, no Python or Bash dependency for core operations.

> **Development status:** repository sync, indexing, search, and installed
> package queries are usable foundations. The emerge-compatible resolver,
> ebuild executor, transactional merge/unmerge, and maintenance operations are
> experimental and are not yet safe replacements for Portage on a live system.
> See [the current audit](AUDIT_2026-07-16.md) and [punch list](PUNCHLIST.md).

## Why

Gentoo's package manager (Portage) is written in Python and Bash. When either
of those breaks — a bad Python upgrade, a corrupted libc, a glibc ABI change —
you lose the ability to install, update, or fix packages. At that point you're
staring at a rescue shell with no package manager.

Portage has also accumulated decades of technical debt: recursive dependency
resolution through a Python interpreter, linear metadata scans, and emergent
complexity from maintaining backwards compatibility across 20+ years of EAPIs.

Arise solves both problems:

- **Survives system breakage**: a single statically-linked Go binary. No Python,
  no Bash, no shared libraries. If your kernel boots, arise works.
- **Performance**: BadgerDB-backed metadata queries replace filesystem scans and
  shell invocations. Dependency resolution runs in-process instead of spawning
  Python for every decision.
- **Unification**: one tool replaces emerge, eix, equery, quickpkg,
  perl-cleaner, python-updater, dispatch-conf, env-update, revdep-rebuild,
  and eselect-news. Learn one CLI, carry one binary.
- **Correctness**: every component has adversarial input tests, mutation tests,
  and an integration framework that compares Arise output against portageq and
  emerge --info to verify 1:1 parity.

The core design rules are:

- **Pure Go, static binary** — `CGO_ENABLED=0`, no dynamic linking.
- **Filesystem is sovereign** — BadgerDB and immutable sidecars accelerate
  queries; they are never the source of truth.
- **Pragmatic fallback** — build phases that require Bash or Make run through
  `os/exec` without making Arise itself dependent on Python.
- **Self-healing** — core inspection and recovery survive system Python
  breakage.

## Quick Start

```sh
# Build
CGO_ENABLED=0 go build -ldflags="-s -w" -o arise ./cmd/arise/

# Sync the repository
arise -repo-url https://... sync

# Index the repo for fast queries
arise index

# Search (replaces eix)
arise search gcc
arise search --installed --category dev-lang
arise search --versions --json python

# List installed atoms (versionless by default)
arise installed
arise installed --versions
arise installed --null
arise installed repo
arise installed no-buildtime
arise installed -= -q all

# Package queries (replaces equery)
arise equery belongs /usr/bin/gcc
arise equery files sys-devel/gcc
arise equery size sys-devel/gcc
arise equery uses sys-devel/gcc
arise equery check sys-devel/gcc
arise equery which sys-devel/gcc

# Install
arise install app-editors/vim

# Update world
arise update

# Audit for outdated Python/Perl packages
arise audit python
arise audit perl --fix

# System info
arise info

# Maintenance
arise depclean --pretend
arise prune --pretend
arise preserved-rebuild
arise revdep-rebuild
arise dispatch-conf
arise env-update
```

## Usage

```text
arise [global-flags] <command> [args...]

Commands:
  sync            Sync the Gentoo repository
  index           Rebuild metadata database from ebuild tree
  install         Install packages
  update          Update @world
  uninstall       Remove packages
  depclean        Remove orphaned packages
  prune           Remove old package versions
  deselect        Remove from @world set
  search          Search packages (replaces eix)
  installed       List installed CP atoms or CPVs (replaces eix-installed)
  query           Look up package metadata
  info            System information (replaces emerge --info)
  equery          Package queries (replaces equery)
  audit           Audit Python/Perl site-packages
  preserved-rebuild  Rebuild after soname changes
  revdep-rebuild  Full reverse dependency scan
  dispatch-conf   List pending config updates
  env-update      Regenerate profile.env
  ldconfig        Update linker cache
  config          Run pkg_config phase
  news            Display Gentoo news (GLEP 42)
  quickpkg        Create binary package
```

## Features

### Performance against Gentoo tools

`emerge` is the behavioral reference. `eix`, `eix-installed`, `equery`, and
related tools are performance references where they provide an equivalent
operation. Correctness-equivalent but slower results fail Arise's benchmark
gate; matching speed is the floor, and the goal is to win decisively.

Initial same-snapshot, correctness-gated results on the development laptop:

| Task | Reference | Equivalent | Arise median | Reference median | Speedup |
|---|---|---:|---:|---:|---:|
| List all installed CPVs | eix-installed | yes | 8.14 ms | 36.86 ms | **4.53x** |
| Firefox substring search | eix | yes | 11.07 ms | 33.01 ms | **2.98x — pass** |
| Firefox substring search | emerge | yes | 11.62 ms | 862.26 ms | **74.22x — pass** |
| Full package index | eix-update | yes | 1.29 s | 4.22 s | **3.27x — pass** |
| No-change package index | eix-update | yes | 713 ms | 4.22 s | **5.92x — pass** |

For that indexed search, Arise uses 32.28 MiB total cache storage versus
eix's 25.80 MiB. Arise's dedicated immutable name index is 451 KiB; the rest
is the richer canonical metadata database used by resolution and queries.
The index comparison uses 30.82 MiB for Arise and 25.77 MiB for eix. Arise's
extra transactional fingerprints make no-change indexing faster than a full
build while preserving package-record/database consistency.

Not yet claimed:

- The isolated `emerge --metadata` workload is ready but still needs a
  privileged run because Portage enforces root/portage-group access.
- Signal Desktop planning remains correctness-blocked; its timing is invalid
  until Arise and emerge produce equivalent plans.

The complete current and planned task matrix is in
[BENCHMARK_MATRIX.md](BENCHMARK_MATRIX.md), with methodology in
[misc/PERFORMANCE.md](misc/PERFORMANCE.md). These values are development
baselines, not cherry-picked release claims.

### emerge compatibility surface (experimental)

The following options and subsystems exist in partial form. This list describes
the intended compatibility surface, not completed behavioral parity.

- Dependency resolution with backtracking (`--backtrack`, `--deep`, `--complete-graph`)
- USE, keyword, license, mask handling from `/etc/portage/`
- Binary packages (XPAK format): create, install, remote binhosts
- `--resume`, `--skipfirst`, `--keep-going`, `--oneshot`, `--nodeps`
- `@world`, `@system`, `@preserved-rebuild`, `@module-rebuild` sets
- FEATURES engine: ccache, distcc, userpriv, split-log, nostrip, fail-clean
- Collision detection, `--noreplace`, `package.provided`

### eix (search) feature parity

- 30+ search filters: category, name, slot, use, keywords, license, regex
- `--versions`, `--json`, `--format`, `--brief`, `--and`/`--not`
- `--depends-on`, `--required-by`, `--has-use`, `--has-version`
- `--care`, `--overflow`, `--masked`, `--duplicates`
- Output modes: JSON, brief, custom format strings, eix-compatible dump

### equery feature parity

- `belongs` — find owning package for a file
- `files` — list installed files
- `uses` — show IUSE and active USE flags
- `size` — total installed size
- `check` — verify checksums and mtimes
- `which` — find ebuild path
- `list` — list installed packages

## Build

```sh
make build          # default build
make static         # static binary (CGO_ENABLED=0)
make test           # run tests
make test-race      # race detector
make test-coverage  # coverage report
make vet            # static analysis
make install        # install to /usr/local
make clean          # remove artifacts
```

Requirements: Go 1.26.3+, Linux (primary target).

## Architecture

```text
cmd/arise/           CLI entry point
internal/
  atom/             Gentoo atom parser (>=cat/pkg-1.0:slot=[use])
  audit/            Python/Perl VDB auditor
  binpkg/           XPAK binary package read/write
  color/            ANSI color output
  depstring/        DEPEND/RDEPEND string parser
  ebuild/           Ebuild file parser
  eclass/           Eclass file parser and inherit resolution
  env/              env-update implementation
  equery/           Package query (belongs, files, uses, etc.)
  features/         FEATURES engine
  fetch/            HTTP source fetcher
  graph/            Dependency graph builder
  ingest/           gob encoding to BadgerDB
  integration/      portageq comparison test framework
  merge/            DESTDIR merge + VDB writing + collision detection
  metadata/         md5-cache parser and PackageMetadata struct
  nameindex/        Immutable, checksummed package-name sidecar
  news/             GLEP 42 news reader
  phase/            Build phase executor (unpack, configure, compile, install)
  perf/             Correctness-gated benchmark harness and reports
  portage/          /etc/portage config parser
  preserved/        @preserved-rebuild and revdep-rebuild scanner
  profile/          Profile inheritance chain parser
  rebuild/          Full rebuild pipeline (fetch -> build -> merge)
  resolve/          Dependency resolver with backtracking
  search/           eix-style package search
  sync/             Repository sync (git + rsync fallback)
  walker/           Parallel md5-cache tree walker
  world/            @world, @system set management
```

## Environment

| Variable | Purpose |
|---|---|
| `ARISE_DB_PATH` | Override database path (default: `/var/lib/arise/data`) |
| `ARISE_REPO_PATH` | Override repository path (default: `/var/db/repos/gentoo`) |
| `NO_COLOR` | Disable colored output |
| `CFLAGS`, `CXXFLAGS`, `MAKEOPTS` | Build configuration |

## License

GPL-3.0 — see [LICENSE](LICENSE) for the full text.
