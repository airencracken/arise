# Arise — Gentoo Package Manager

A high-performance package-manager control plane for Gentoo Linux, written in
Go. Core state inspection, configuration evaluation, resolution and fetching
do not require Portage's Python runtime. Ebuild execution intentionally uses
Bash and remains experimental.

> **Development status:** repository sync, indexing, search, and installed
> package queries are usable foundations. The emerge-compatible resolver,
> ebuild executor, transactional merge/unmerge, and maintenance operations are
> experimental and are not yet safe replacements for Portage on a live system.
> See [the latest development checkpoint](docs/audits/CHECKPOINT_2026-07-18.md),
> [documentation index](docs/README.md), and [punch list](PUNCHLIST.md).
> The moving-target test policy is documented in
> [the compatibility contract](COMPATIBILITY.md).

## Why

Portage is the authoritative package manager for Gentoo and the source of much
of what Arise knows about package-management semantics. Its long compatibility
history, broad EAPI support, and integration with the rest of Gentoo are
formidable engineering achievements.

Arise explores a complementary architecture with two narrower goals. First, a
static control plane may remain available when Python or other parts of the
normal package-management environment need repair. Second, indexed immutable
state and in-process resolution may reduce latency for read-heavy operations.
Neither goal relaxes compatibility or safety requirements.

Arise is therefore being built around these priorities:

- **Recovery-oriented control plane**: one statically linked Go binary can
  inspect package state and construct verified plans when Portage's Python
  environment is damaged. Safe live repair still requires the unfinished
  execution and transaction milestones.
- **Performance**: BadgerDB-backed metadata queries can avoid repeated
  filesystem scans and shell invocations. Dependency resolution runs in one
  process over an immutable snapshot.
- **Unified direction**: one static tool is intended to offer familiar
  workflows from emerge, eix, equery, quickpkg, perl-cleaner, python-updater,
  dispatch-conf, env-update, revdep-rebuild, and eselect-news.
- **Cohesive Gentoo toolbox**: Arise is intentionally broader than a narrowly
  scoped command. Shared package atoms, configuration, snapshots, resolution,
  repair, execution and audit primitives should compose into many focused
  workflows without duplicating policy or requiring the normal runtime to be
  healthy. Internally those primitives remain small, testable Go libraries;
  dangerous operations cross explicit plan, verification and journal gates.
- **Correctness gates**: deterministic tests and live differential corpora
  compare package state, policy, plans and benchmarks with Portage. Unsupported
  execution fails closed rather than reporting success.

The core design rules are:

- **Pure Go, static binary** — `CGO_ENABLED=0`, no dynamic linking.
- **Filesystem is sovereign** — BadgerDB and immutable sidecars accelerate
  queries; they are never the source of truth.
- **Pragmatic fallback** — build phases that require Bash or Make run through
  `os/exec` without making Arise itself dependent on Python.
- **Recovery-oriented** — core inspection and planning remain available when
  the system Python environment is unavailable.

## Foundations and acknowledgements

Arise stands on the work of Gentoo, Portage, and the wider Gentoo tooling
ecosystem. Portage defines the behavioral reference; eix, Gentoolkit,
portage-utils, pkgcore/pkgcheck, and other projects provide both inspiration
and valuable comparison points. Their maintainers have solved difficult
package-management, compatibility, migration, and recovery problems over many
years.

This is a small experimental project maintained by one developer with AI
assistance. Where Arise differs architecturally, that is an experiment to
measure and validate—not a dismissal of the tradeoffs made by the engineers
whose work made Gentoo and this project possible. Compatibility claims should
be supported by differential tests, and performance claims by equivalent
same-snapshot workloads.

## Quick Start

```sh
# Build
CGO_ENABLED=0 go build -ldflags="-s -w" -o arise ./cmd/arise/

# Sync the repository
arise -repo-url https://... sync

# Index the repo for fast queries
arise index

# Indexed search with eix-familiar workflows
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

# Inspect every repository/version candidate for one package
arise query --versions www-client/firefox

# Deterministic state used for Portage differential checks
arise state json
arise state available-cpv
arise state installed-cpv

# Package queries with equery-familiar workflows
arise equery belongs /usr/bin/gcc
arise equery files sys-devel/gcc
arise equery size sys-devel/gcc
arise equery uses sys-devel/gcc
arise equery check sys-devel/gcc
arise equery which sys-devel/gcc

# Resolve an install without mutating the system
arise --pretend install app-editors/vim

# Resolve an @world update
arise --pretend update

# Read-only audit for outdated Python/Perl packages
arise audit python
arise audit perl

# System info
arise info

# Maintenance
arise depclean --pretend
arise prune --pretend
# Read-only maintenance proposals
arise --pretend preserved-rebuild
arise --pretend revdep-rebuild
arise dispatch-conf
```

Non-pretend install, update, uninstall, depclean, repair and rebuild execution
is deliberately blocked until the versioned ebuild ABI and journaled
transaction engine satisfy their punch-list gates. Source `--fetchonly` is the
current bounded exception and consumes only Manifest-verified artifacts.

## Usage

```text
arise [global-flags] <command> [args...]

Commands:
  sync            Sync the Gentoo repository
  index           Rebuild metadata database from ebuild tree
  install         Resolve package installation (execution gated)
  update          Resolve an @world update (execution gated)
  uninstall       Propose package removal (execution gated)
  depclean        Propose orphan removal (execution gated)
  prune           Propose old-version removal (execution gated)
  deselect        Remove from @world set
  search          Search packages (eix-familiar surface)
  installed       List installed CP atoms or CPVs
  query           Look up package metadata
  info            Partial system information
  equery          Package queries (equery-familiar surface)
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

### Performance comparisons with Gentoo tools

`emerge` is the behavioral reference. `eix`, `eix-installed`, `equery`, and
related tools are performance references where they provide an equivalent
operation. These comparisons are intended to validate Arise's architectural
choices, not to diminish mature tools with broader responsibilities. Arise's
own benchmark gate rejects correctness-equivalent regressions and requires a
material measured benefit before making a performance claim.

Same-snapshot, correctness-gated checkpoint results from 2026-07-17:

| Task | Reference | Equivalent | Arise median | Reference median | Speedup |
|---|---|---:|---:|---:|---:|
| List all installed CPVs | eix-installed | yes | 6.42 ms | 37.47 ms | **5.83x** |
| Firefox substring search | eix | yes | 11.69 ms | 33.95 ms | **2.90x** |
| Firefox substring search | emerge | yes | 10.95 ms | 865.47 ms | **79.03x** |
| Signal Desktop dependency plan | emerge | yes | 1.30 s | 3.30 s | **2.54x** |
| Shallow `@system` dependency plan | emerge | yes (11/11 actions) | 2.26 s | 6.42 s | **2.84x** |
| Crash-safe full configured-repository index | eix-update | yes | 3.96 s | 4.26 s | **1.08x** |
| Crash-safe no-change configured-repository index | eix-update | yes | 1.86 s | 4.26 s | **2.29x** |

The index comparison covers every configured repository on both sides and
validates normalized package names after each build. Current indexing creates a
complete immutable generation, publishes it atomically and retains a rollback
generation. Cache-footprint publication is temporarily withheld because the
harness must follow generation symlinks before its numbers are trustworthy.

Not yet claimed:

- The isolated `emerge --metadata` workload is ready but still needs a
  privileged run because Portage enforces root/portage-group access.
- `@world` planning remains correctness-blocked on the intentionally damaged
  development snapshot; the current hard gate is an installed live
  `llvm-core/llvm-23.0.0.9999:23/23.0` without an installable matching
  candidate, so no speedup is published.

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

### Search surface

- 30+ search filters: category, name, slot, use, keywords, license, regex
- `--versions`, `--json`, `--format`, `--brief`, `--and`/`--not`
- `--depends-on`, `--required-by`, `--has-use`, `--has-version`
- `--care`, `--overflow`, `--masked`, `--duplicates`
- Output modes: JSON, brief, custom format strings, eix-compatible dump

### Installed-package query surface

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
make test-live-portage-compile # compile the opt-in host comparison lane
make test-integration # read-only live Portage comparisons
make vet            # static analysis
make install        # install to /usr/local
make clean          # remove artifacts
```

Requirements: Go 1.26.3+, Linux (primary target).

The default suite is hermetic: it does not invoke host Portage tools or open
loopback listeners. Live comparisons are opt-in and timeout-bounded. See
[`docs/testing/TEST_LANES.md`](docs/testing/TEST_LANES.md).

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
  distfiles/        Manifest parsing and artifact verification
  fetch/            Atomic, mirror-aware verified source acquisition
  graph/            Dependency graph builder
  ingest/           gob encoding to BadgerDB
  integration/      portageq comparison test framework
  merge/            DESTDIR merge + VDB writing + collision detection
  metadata/         md5-cache parser and PackageMetadata struct
  nameindex/        Immutable, checksummed package-name sidecar
  news/             GLEP 42 news reader
  phase/            Build phase executor (unpack, configure, compile, install)
  phaseproto/       Versioned isolated Go-to-Bash ebuild protocol
  oplock/           Portage-compatible operation locking
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
| `PORTDIR` | Default repository path; overridden by `--repo` |
| `PORTAGE_CONFIGROOT`, `ROOT`, `SYSROOT`, `BROOT` | Portage root selectors |
| `DISTDIR`, `PKGDIR`, `PORTAGE_TMPDIR` | Storage/build path defaults |
| `NO_COLOR` | Disable colored output |
| `USE`, `FEATURES`, policy and toolchain variables | Allowlisted one-shot Portage configuration; see the P2 contract |

## License

GPL-3.0 — see [LICENSE](LICENSE) for the full text.
