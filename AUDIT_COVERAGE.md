# Coverage Gaps by Package

## Low Coverage Packages (< 80%)

| Package | Coverage | Missing |
|---------|----------|---------|
| benchmark | 0.0% | Benchmark tests only, no unit tests |
| integration | 0.8% | Requires live Gentoo tree |
| sync | 34.0% | Git/rsync operations untested |
| phase | 79.5% | Phase execution logic |
| binpkg | 74.5% | XPAK read/write, download |
| preserved | 76.3% | revdep/preserved rebuild logic |
| rebuild | 81.4% | Parallel rebuild, load control |
| resolve | 79.6% | Complex resolver paths |

## Untested Functions in sync/sync.go (0% Coverage)

```go
func Sync(ctx context.Context, cfg SyncConfig) error
func cloneGitRepo(ctx context.Context, url, targetDir string) error
func updateGitRepo(ctx context.Context, targetDir string) error
func syncRsync(ctx context.Context, url, targetDir string) error
func GitAvailable() bool
func isGitRepo(dir string) bool
func Validate() error
func defaults() SyncConfig
```

## Untested Code Paths in resolve/resolve.go

- `processCompleteGraph()` - slot operator rebuild logic
- `AutoUnmask()` / `AutoAcceptLicense()` - config writing
- `SaveResume()` / `LoadResume()` / `MarkResumeComplete()` / `SkipFirstResume()` - resume file ops
- `Depclean()` / `Prune()` - orphan/old package removal
- `CheckRequiredUse()` - REQUIRED_USE validation
- `LicenseAccepted()` - license acceptance logic
- Blocking logic in `processBlock()`
- Any-of group resolution in `processAnyOf()`

## Untested Code Paths in graph/graph.go

- `BuildParallel()` - parallel graph building
- `FindOutdated()` - outdated dependency detection
- `ReverseDepsOf()` - reverse dependency queries
- `walkCache()` - md5-cache walking

## Integration Test Gap

`internal/integration/` only runs with live Gentoo tree at `/var/db/repos/gentoo/metadata/md5-cache`. No CI/CD integration.

## Recommendation

1. Add unit tests for `sync` package with mocked git/rsync
2. Add tests for `resolve` resume/autounmask paths
3. Add tests for `graph` parallel building
4. Set up CI with minimal Gentoo tree for integration tests