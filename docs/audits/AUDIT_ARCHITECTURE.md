# Architecture Review

> Historical development audit; retain for context until superseded or pruned.

## Strengths

### 1. Clean Package Separation (26 internal packages)
Each package has single responsibility:
- `atom` - Atom parsing/comparison (CPV, version, slots, USE)
- `depstring` - DEPEND/RDEPEND parsing (any-of, use-conditionals, blocks)
- `ebuild`/`eclass` - Ebuild/eclass parsing
- `graph` - Dependency graph construction from metadata
- `resolve` - Backtracking resolver with full emerge parity
- `rebuild` - Build orchestration (fetch → phases → merge)
- `phase` - Build phase execution (unpack, configure, compile, install)
- `merge` - DESTDIR merge + VDB writing + collision detection
- `binpkg` - XPAK binary package read/write/download
- `portage` - /etc/portage config parsing
- `search` - eix-style package search
- `walker` - Parallel md5-cache ingestion
- `ingest` - BadgerDB encoding/decoding
- `world` - @world/@system set management

### 2. Database as Acceleration Layer
- BadgerDB stores parsed metadata for fast queries
- Filesystem (md5-cache, VDB) remains source of truth
- `ingest` package handles gob encoding/decoding

### 3. Static Binary Goal
- `CGO_ENABLED=0` in Makefile
- No Python/Bash dependency for core ops
- Only `os/exec` for build phases needing bash/make

### 4. Comprehensive Flag Support
1564-line main.go supports full emerge flag set:
- Resolution: `--backtrack`, `--deep`, `--complete-graph`, `--newuse`, `--changed-use`, `--changed-deps`
- Binary: `--buildpkg`, `--usepkg`, `--getbinpkg`, `--binpkg-respect-use`
- Output: `--pretend`, `--ask`, `--quiet`, `--verbose`, `--tree`, `--json`
- Resume: `--resume`, `--skipfirst`
- Jobs/Load: `-j`, `--load-average`, `--keep-going`

## Gaps and Issues

### 1. Main.go Too Large (1564 lines)
- All command handlers in single file
- Flag definitions mixed with logic
- No unit tests for CLI layer
- Hard to maintain/extend

**Recommendation**: Split into subcommands:
```
cmd/arise/
  main.go           # Entry, flag parsing
  sync.go           # sync command
  index.go          # index command
  install.go        # install/update commands
  query.go          # query/search/info
  audit.go          # audit command
  maintain.go       # depclean/prune/preserved-rebuild/revdep-rebuild
  config.go         # config/env-update/ldconfig/news/dispatch-conf
  equery.go         # equery subcommands
  quickpkg.go       # quickpkg command
```

### 2. No Interface Abstractions
- Concrete types used throughout (e.g., `*badger.DB`, `*resolve.DepGraph`)
- Hard to mock for unit tests
- Tight coupling between packages

**Example**: `graph.Build()` returns `*DepGraph` which `resolve.Resolve()` consumes directly.

### 3. Hardcoded Paths
```go
// In main.go and rebuild.go:
DistfilesDir: "/var/cache/distfiles"
RootDir:      "/"
VdbDir:       "/var/db/pkg"
WorkDirBase:  "/var/tmp/arise"
BinpkgDir:    "/var/cache/binpkgs"
PortageConfigRoot: "/etc/portage"
ResumePath:   "/var/tmp/arise/resume"
```

Should use environment variables or config file.

### 4. Phase Execution Limited
`internal/phase/phase.go` only supports:
- `src_unpack` - tar extraction
- `src_prepare` - no-op
- `src_configure` - autoconf only
- `src_compile` - make only
- `src_install` - make install only

Missing: `pkg_preinst`, `pkg_postinst`, `pkg_prerm`, `pkg_postrm`, `src_test`, custom EAPI phases.

### 5. No Plugin/Extension System
- All functionality compiled in
- Can't add custom fetchers, merge strategies, resolvers
- Unlike Portage's eclass/system

### 6. Error Handling Inconsistent
- Some functions return `(T, error)`
- Others use callbacks (`OnError`, `OnPhaseStart`)
- Context cancellation not always checked in loops

### 7. Single-Threaded Resolver
`resolve.Resolve()` is sequential despite `-j` flag for builds.
Backtracking is inherently sequential but dependency graph could be partitioned.

### 8. Missing Features (per README claims)
- `@system`, `@module-rebuild` sets - not fully implemented
- `package.provided` - parsed but not fully integrated
- FEATURES: `ccache`, `distcc`, `userpriv`, `split-log`, `nostrip`, `fail-clean` - partially in `features/`
- Collision detection - in `merge/collision.go` but not fully integrated
- `dispatch-conf` - only lists, doesn't merge

### 9. Vendor Directory Committed
`vendor/` checked in (42 indirect deps). Go modules should handle this.

### 10. No Structured Logging
- Uses `fmt.Fprintf` to stderr/stdout
- No log levels, structured output, or sampling
- Hard to debug in production

## Recommendations

1. **Refactor main.go** into command subpackages
2. **Add interfaces** for DB, Graph, Resolver, Fetcher, Merger
3. **Externalize paths** via config file/env vars
4. **Complete phase implementation** for all EAPI phases
5. **Add structured logging** (slog or zerolog)
6. **Remove vendor/** from git, use Go modules
7. **Add integration test CI** with minimal Gentoo tree
