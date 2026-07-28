# Development

## Build and test

```sh
make build
make test
make test-race
make test-coverage
make test-live-portage-compile
make test-integration
make vet
```

Requirements: Go 1.26.3+ and Linux.

The Gentoo ebuild produces a static recovery binary by default. The opt-in
`pie` USE flag selects Gentoo's normal position-independent hardening tradeoff.
The default test suite is hermetic; live comparisons are opt-in and
timeout-bounded. See [`testing/TEST_LANES.md`](testing/TEST_LANES.md).

## Architecture

```text
cmd/arise/           CLI entry point
internal/
  atom/             Gentoo atom and version parser
  binpkg/           XPAK/GPKG packages, indexes, and recovery capture
  depstring/        Dependency expression parser
  distfiles/        Manifest parsing and artifact verification
  executor/         Dependency-aware action scheduler
  graph/            Dependency graph construction
  ingest/           Metadata indexing into BadgerDB
  journal/          Durable package transaction journal
  merge/            DESTDIR merge, VDB writing, and collision detection
  metadata/         md5-cache parsing and package metadata
  perf/             Correctness-gated benchmark reports
  phaseproto/       Versioned isolated Go-to-Bash ebuild protocol
  portage/          Portage configuration parsing
  rebuild/          Fetch, build, and merge pipeline
  resolve/          Dependency resolver with backtracking
  search/           Indexed package search
  snapshotstore/    Immutable snapshot publication and retention
  sync/             Repository synchronization
  vdb/              Installed package metadata and ownership
  world/            World and system set management
```

The remaining internal packages provide focused query, recovery, configuration,
audit, and compatibility primitives. Package documentation and tests are the
authoritative implementation-level reference.

## Environment

| Variable | Purpose |
|---|---|
| `PORTDIR` | Default repository path; overridden by `--repo` |
| `PORTAGE_CONFIGROOT`, `ROOT`, `SYSROOT`, `BROOT` | Portage root selectors |
| `DISTDIR`, `PKGDIR`, `PORTAGE_TMPDIR` | Storage and build paths |
| `NO_COLOR` | Disable colored output |
| `USE`, `FEATURES`, policy and toolchain variables | Allowlisted one-shot Portage configuration |

Arise evaluates the declarative shell-assignment subset used by normal Portage
configuration. It does not execute arbitrary shell functions, conditionals,
command substitutions, or sourced scripts.

Arise reads `/etc/portage` as shared Gentoo policy, while Arise-specific
settings belong under `/etc/arise`. See
[`configuration-layout.md`](configuration-layout.md) for path ownership.
