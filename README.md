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

- **Pure Go, static binary by default** — `CGO_ENABLED=0`, with the packaged
  recovery build overriding Gentoo's normal PIE default so its control plane
  does not depend on the host dynamic loader or shared-library state. PIE is a
  sound general hardening default and remains available through the overlay's
  opt-in `pie` USE flag.
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

## Install from the Arise overlay

The maintained [Arise overlay](https://github.com/airencracken/arise-overlay)
is the recommended way to install a packaged release on Gentoo; no manual Go
build is required. Add and synchronize it with Portage:

```sh
eselect repository add arise-overlay git \
  https://github.com/airencracken/arise-overlay.git
emaint sync -r arise-overlay
```

Stable releases remain testing-keyworded while Arise is experimental. Add this
package-specific acceptance:

```text
# /etc/portage/package.accept_keywords/arise
sys-apps/arise ~amd64
```

Then install Arise normally:

```sh
emerge --ask sys-apps/arise
```

That Portage bootstrap is only for systems where Arise is not installed yet.
An existing Arise installation updates itself and its overlay directly:

```sh
arise sync
arise -1 --reinstall =sys-apps/arise-0.0.5
```

To synchronize only selected configured repositories, name them explicitly:

```sh
arise sync arise-overlay
```

Sync output summarizes one repository per line and reports package/version-set
changes with eix-style tags: `[N]` new, `[D]` deleted, `[U]` upgraded, `[>]`
versions added, `[<]` versions removed or downgraded, and `[C]` metadata
changed. Use `-v` for transport and per-ebuild diagnostics.

For source development:

```sh
# Build
CGO_ENABLED=0 go build -ldflags="-s -w" -o arise ./cmd/arise/

# Sync the repository
arise --repo-url https://... sync

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
arise maintain world --check
# Repair immediately; --fix is explicit mutation authorization
arise maintain world --fix
# Optionally review and bind the repair to a saved state snapshot
arise --pretend --save-plan world-repair maintain world --fix
arise --approve-plan world-repair maintain world --fix
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
  sync [repo...]  Sync all or selected configured repositories
  index           Rebuild metadata database from ebuild tree
  install         Resolve package installation (execution gated)
  update          Resolve an @world update (execution gated)
  uninstall       Propose package removal (execution gated)
  recover         Inspect journals; inspect, restore, verify, or prune recovery sets
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
  maintain        Check or repair package-manager state
  preserved-rebuild  Rebuild after soname changes
  revdep-rebuild  Full reverse dependency scan
  dispatch-conf   Review and merge protected config updates
  env-update      Regenerate profile.env
  ldconfig        Update linker cache
  config          Run pkg_config phase
  news            Display Gentoo news (GLEP 42)
  quickpkg        Create recovery binpkg or Portage-compatible GPKG
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

### 0.0.5 resolver tuning

The 0.0.5 C2 tuning cycle attacked allocation and garbage-collection pressure
in the large resolver path. On the same warm, read-only, deep/newuse,
complete-graph `@world` workload, with exact plan and whole-state digests
preserved:

| Metric | Initial C2 baseline | Arise 0.0.5 | Improvement |
|---|---:|---:|---:|
| Median wall time | 3.45 s | 2.48 s | **28.1% faster** |
| Median CPU time | 7.72 s | 4.63 s | **40.0% lower** |
| Median peak RSS | 812,612 KiB | 568,604 KiB | **30.0% lower** |
| Profiled allocation | 2,645.55 MiB | 733.20 MiB | **72.3% lower** |

Those gains come from buffer reuse, cached implicit USE expansion prefixes, a
resolver-specific VDB projection, streaming dependency metadata, and direct
handoff of already parsed dependency atoms. Candidates that regressed speed or
memory were removed. The detailed methodology and immutable result digests are
in the [0.0.5 release notes](docs/releases/0.0.5.md).

The full Gentoo-tool comparison tables, memory methodology, damaged-state
diagnostic, evidence links, and claim boundaries now live in
[the performance results](docs/performance-results.md).

### emerge compatibility surface (experimental)

The following options and subsystems exist in partial form. This list describes
the intended compatibility surface, not completed behavioral parity.

- Dependency resolution with backtracking (`--backtrack`, `--deep`, `--complete-graph`)
- USE, keyword, license, mask handling from `/etc/portage/`
- Binary packages: Portage-compatible XPAK/GPKG selection and transactional
  install, deterministic GPKG creation, and verified Packages-indexed binhosts
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

## Development

Build with `make build`, validate with `make test && make vet`, and see
[the development guide](docs/development.md) for test lanes, architecture,
environment variables, and configuration boundaries.

## License

GPL-3.0 — see [LICENSE](LICENSE) for the full text.
