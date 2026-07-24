# Arise — Gentoo Package Manager

A high-performance package-manager control plane for Gentoo Linux, written in
Go. Core state inspection, configuration evaluation, resolution and fetching
do not require Portage's Python runtime. Ebuild execution intentionally uses
Bash through Arise's versioned phase protocol.

> **Development status:** repository sync, indexing, search, installed-package
> queries, verified planning, source rebuilds, and journaled live-root package
> transactions are implemented. Resolver, execution, recovery, and maintenance
> compatibility remain incomplete and Arise is not yet a general replacement
> for Portage. Install, update, reinstall, uninstall, select, and deselect use
> journaled live mutation directly; saved plans remain available for optional
> state-bound review and deferred execution. See the
> [Portage self-hosting milestone](docs/evidence/PORTAGE_SELF_HOSTING_MILESTONE_2026-07-24.md),
> [documentation index](docs/README.md), and [punch list](PUNCHLIST.md).
> The moving-target test policy is documented in
> [the compatibility contract](COMPATIBILITY.md).

> **Experimental live package manager:** Arise now executes supported package
> transactions directly, but the project remains experimental and the overlay
> release is testing-keyworded for `~amd64`. Keep Portage installed as the
> reference and recovery implementation while Arise completes its compatibility
> and fresh-stage3 acceptance gates.

## Why

Portage is the authoritative package manager for Gentoo and the source of much
of what Arise knows about package-management semantics. Its long compatibility
history, broad EAPI support, and integration with the rest of Gentoo are
formidable engineering achievements.

Arise aims to be correct, useful, performant, and reliable: a "Swiss army
chainsaw" for taking an old or damaged Gentoo installation to a clean,
verified, current state without discarding its administrator's unusual but
valid choices. A static control plane remains available when Python or other
parts of the normal package-management environment need repair. Indexed,
immutable state and Go concurrency avoid serializing expensive control-plane
work behind a language runtime lock. Neither recovery nor performance relaxes
compatibility or safety requirements.

Arise is therefore being built around these priorities:

- **Recovery-oriented control plane**: one statically linked Go binary can
  inspect package state and construct verified plans when Portage's Python
  environment is damaged. Live repair crosses explicit verified
  plan, preflight, operation-lock, journal, and recovery gates.
- **Performance**: BadgerDB-backed metadata queries can avoid repeated
  filesystem scans and shell invocations. Dependency resolution runs in one
  process over an immutable snapshot and is designed to use independent CPU
  work concurrently while retaining deterministic output. Performance gates
  compare only equivalent verified results.
- **Whole-state repair**: damaged-world analysis derives the complete ordered
  repair closure and explains every rebuild, replacement, or removal. The
  operator should not have to recover by repeatedly widening hand-selected
  `--oneshot --nodeps` batches until the dependency graph happens to converge.
- **Unusual systems remain first-class**: split-usr, overlays, old slots,
  preserved libraries, custom USE policy, and other supported Gentoo choices
  are compatibility inputs—not excuses to bypass verification or recovery.
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
- **Optional integrations stay optional** — an enhancement that adds an Arise
  runtime dependency must retain a functional baseline fallback and be exposed
  through an explicit Gentoo USE flag. Bubblewrap and filesystem snapshot
  providers may strengthen execution or recovery, but cannot gate ordinary
  control-plane or Portage-compatible behavior.
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

Gentoo users can install the latest packaged release from the maintained
[Arise overlay](https://github.com/airencracken/arise-overlay):

```sh
eselect repository add arise-overlay git \
  https://github.com/airencracken/arise-overlay.git
emaint sync -r arise-overlay
emerge --ask sys-apps/arise
```

For source development:

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

Live-root install, update, reinstall, and uninstall execute directly after a
verified plan. Explicit install targets are selected into world unless
`--oneshot` is set. `--save-plan` and `--approve-plan` provide optional
state-bound review and deferred execution. Source transactions use
Manifest-verified artifacts, isolated phase workers, durable per-package logs,
collision checks, package journals, operation locking, and resumable
dependency-aware scheduling. Package preparation may run concurrently, while
ROOT and VDB commits remain serialized. Depclean, repair, and broader
maintenance mutation remain unavailable until their complete plans and rollback
boundaries join the same transaction model.

## Usage

```text
arise [global-flags] <command> [args...]

Commands:
  sync            Sync the Gentoo repository
  index           Rebuild metadata database from ebuild tree
  install         Resolve package installation (execution gated)
  update          Resolve an @world update (execution gated)
  uninstall       Propose package removal (execution gated)
  recover         Inspect or roll back durable package journals
  depclean        Propose orphan removal (execution gated)
  prune           Propose old-version removal (execution gated)
  select          Add an installed package to @world
  deselect        Remove from @world set
  search          Search packages (eix-familiar surface)
  installed       List installed CP atoms or CPVs
  query           Look up package metadata
  state           Emit deterministic repository/VDB comparison state
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
  bench           Run built-in microbenchmarks
```

## Features

### Performance comparisons with Gentoo tools

`emerge` is the behavioral reference. `eix`, `eix-installed`, `equery`, and
related tools are performance references where they provide an equivalent
operation. These comparisons are intended to validate Arise's architectural
choices, not to diminish mature tools with broader responsibilities. Arise's
own benchmark gate rejects correctness-equivalent regressions and requires a
material measured benefit before making a performance claim.

Same-snapshot, correctness-gated checkpoint results through 2026-07-18:

| Task | Reference | Equivalent | Arise median | Reference median | Speedup |
|---|---|---:|---:|---:|---:|
| List all installed CPVs | eix-installed | yes | 6.42 ms | 37.47 ms | **5.83x** |
| Firefox substring search | eix | yes | 11.69 ms | 33.95 ms | **2.90x** |
| Firefox substring search | emerge | yes | 10.95 ms | 865.47 ms | **79.03x** |
| Shallow `@system` update plan | emerge | yes (11/11 actions) | 1.43 s | 6.48 s | **4.52x** |
| Crash-safe full configured-repository index | eix-update | yes | 3.96 s | 4.26 s | **1.08x** |
| Crash-safe no-change configured-repository index | eix-update | yes | 1.86 s | 4.26 s | **2.29x** |

Damaged-state recovery is reported separately because unequal outcomes cannot
enter the equivalence speedup table. On repository commit
`dbc31827cd0aab0d3b90114899a2eb2136dcb726`, three uninstrumented runs of deep,
newuse, complete-graph `@system` with build dependencies produced:

| Resolver | Outcome | Actions | Median wall time |
|---|---|---:|---:|
| Arise | verified repair, zero conflicts | 159 | **2.05 s** |
| Portage 3.0.77 | unresolved partial plan, slot conflicts and four unsatisfied blocks | 143 displayed | 27.34 s |

Arise used 13.33x less wall time in this damaged state while producing the
stronger verified outcome. This is deliberately labeled a recovery diagnostic,
not a Portage-equivalent speedup. The three samples, commands, outcomes, binary
digest and repository commit are preserved in
[`P3_SYSTEM_REPAIR_TIMINGS_2026-07-18.json`](docs/evidence/P3_SYSTEM_REPAIR_TIMINGS_2026-07-18.json).

The index comparison covers every configured repository on both sides and
validates normalized package names after each build. Current indexing creates a
complete immutable generation, publishes it atomically and retains a rollback
generation. Cache-footprint publication is temporarily withheld because the
harness must follow generation symlinks before its numbers are trustworthy.
The dated `@system` samples and normalized outcome record are preserved in
[`P3_SYSTEM_SHALLOW_TIMINGS_2026-07-18.json`](docs/evidence/P3_SYSTEM_SHALLOW_TIMINGS_2026-07-18.json).

Not yet claimed:

- The isolated `emerge --metadata` workload is ready but still needs a
  privileged run because Portage enforces root/portage-group access.
- A 2026-07-18 deep, newuse, complete-graph `@world` snapshot produced an exact
  369-action comparison with Portage and a successful whole-plan preflight.
  No resolver speedup is published yet because that result has not completed
  the repeated, correctness-gated benchmark protocol.
- A successful deep `@world` execution is not yet evidence of
  general Portage execution parity. Parallel scheduling, fetch policy,
  lifecycle coverage, failure recovery, and long-running mutation still need
  broader machine and package-corpus coverage.
- The current deep/newuse `@system` outcome is ineligible for the equivalence
  table because Arise repairs the state while Portage reports an unresolved
  partial plan. Its separately labeled recovery diagnostic appears above.
- Signal Desktop remains useful as a binary-package and user-patch regression,
  but is no longer treated as a representative resolver or execution benchmark.
  Resolver performance work now uses the explicit-package, `@system`, normal
  `@world`, damaged-world, and empty-tree matrix.

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
make build          # verified static binary (CGO_ENABLED=0)
make static         # alias for the canonical static build
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

The Gentoo ebuild enables the `static` USE flag by default. Explicitly
disabling it opts into a cgo-enabled dynamic build; the default installation
retains the standalone recovery guarantee.

The default suite is hermetic: it does not invoke host Portage tools or open
loopback listeners. Live comparisons are opt-in and timeout-bounded. See
[`docs/testing/TEST_LANES.md`](docs/testing/TEST_LANES.md).

## Architecture

```text
cmd/arise/           CLI entry point
internal/
  atom/             Gentoo atom parser (>=cat/pkg-1.0:slot=[use])
  audit/            Python/Perl VDB auditor
  benchmark/        Live/reference comparison workloads
  binpkg/           XPAK binary package read/write
  color/            ANSI color output
  depstring/        DEPEND/RDEPEND string parser
  distfiles/        Manifest parsing and artifact verification
  ebuild/           Ebuild file parser
  eclass/           Eclass file parser and inherit resolution
  env/              env-update implementation
  equery/           Package query (belongs, files, uses, etc.)
  executor/         Dependency-aware parallel action scheduler
  features/         FEATURES engine
  fetch/            Atomic, mirror-aware verified source acquisition
  graph/            Dependency graph builder
  ingest/           gob encoding to BadgerDB
  installedquery/   Constrained ROOT/BROOT installed-package queries
  integration/      portageq comparison test framework
  journal/          Durable package transaction journal and recovery
  lifecycletrace/   Optional lifecycle filesystem syscall capture
  log/              Structured logging primitives
  merge/            DESTDIR merge + VDB writing + collision detection
  metadata/         md5-cache parser and PackageMetadata struct
  nameindex/        Immutable, checksummed package-name sidecar
  news/             GLEP 42 news reader
  oplock/           Portage-compatible operation locking
  packagestate/     Mutation authorization state fingerprints
  perf/             Correctness-gated benchmark reports
  phase/            Build phase executor (unpack, configure, compile, install)
  phaseproto/       Versioned isolated Go-to-Bash ebuild protocol
  plancompare/      Normalized saved-plan comparison
  portage/          /etc/portage config parser
  preserved/        @preserved-rebuild and revdep-rebuild scanner
  profile/          Profile inheritance chain parser
  rebuild/          Full rebuild pipeline (fetch -> build -> merge)
  repoaudit/        Whole-repository ebuild/eclass compatibility audit
  resolve/          Dependency resolver with backtracking
  resolversnapshot/ Portable resolver-state snapshots
  search/           eix-style package search
  snapshotstore/    Immutable snapshot publication and retention
  sync/             Repository sync (git + rsync fallback)
  vdb/              Installed package metadata and ownership records
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

Arise evaluates the declarative shell-assignment subset used by normal Portage
configuration: quoting, continuations, ordered assignments, and `$VAR` or
`${VAR}` references across profile defaults, `make.conf`, and `package.env`.
It does not execute arbitrary shell functions, conditionals, command
substitutions, or sourced scripts; unsupported dynamic configuration must not be
silently treated as an equivalent Portage environment.

Arise reads `/etc/portage` as shared Gentoo policy, but Arise-specific settings
belong under `/etc/arise`; see
[`docs/configuration-layout.md`](docs/configuration-layout.md) for configuration,
state, cache, log, runtime, and temporary-file ownership.

## License

GPL-3.0 — see [LICENSE](LICENSE) for the full text.
