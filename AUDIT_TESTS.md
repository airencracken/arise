# Test Quality Audit

## Test Categories Present

| Category | Location | Status |
|----------|----------|--------|
| Unit | `*_test.go` in each package | ✅ Comprehensive |
| Adversarial | `*_test.go` (Adversarial/Mutation tests) | ✅ Present |
| Mutation | `*_test.go` (Mutation tests) | ✅ Present |
| Property | Not found | ❌ Missing |
| Integration | `internal/integration/` | ⚠️ Requires live tree |
| Benchmark | `internal/benchmark/` | ✅ Present |
| Schema/API | Not found | ❌ Missing |
| Route/CLI | Not found | ❌ Missing |

## Test Quality by Package

### Excellent (≥ 90% coverage, adversarial tests)
- `atom` - Parser fuzzing, version comparison edge cases
- `metadata` - Cache entry parsing, malformed entries
- `color` - ANSI code generation
- `ebuild` - Phase parsing, variable extraction
- `news` - GLEP 42 parsing, read/unread tracking

### Good (80-90% coverage)
- `audit` - Python/Perl site-packages detection
- `depstring` - Complex dependency parsing (||, use?, blocks)
- `eclass` - Inheritance resolution
- `env` - env-update, ldconfig
- `equery` - VDB queries (belongs, files, uses, size, check)
- `features` - FEATURES parsing, split-log, distcc
- `fetch` - HTTP fetch, resume, mirror handling
- `graph` - Graph building, revdeps, updates
- `ingest` - BadgerDB encode/decode
- `merge` - File merging, collision detection
- `portage` - make.conf, package.* parsing
- `preserved` - Library preservation, revdep rebuild
- `profile` - Profile inheritance
- `search` - eix-style filters, output formats
- `walker` - Parallel cache walking
- `world` - @world set management

### Needs Work (< 80% or missing tests)
- `binpkg` (74.5%) - XPAK read/write, download, USE matching
- `phase` (79.5%) - Phase execution, env building
- `rebuild` (81.4%) - **Data race in parallel test**, orchestration
- `resolve` (79.6%) - Complex resolver paths
- `sync` (34.0%) - **No git/rsync tests**
- `integration` (0.8%) - **No CI integration**

## Missing Test Types

### 1. Property-Based Tests
No use of `testing/quick` or `gopter` for:
- Atom parsing round-trip (parse → string → parse)
- Version comparison transitivity
- Dependency resolution idempotency
- Merge/unmerge round-trip

### 2. Schema Validation Tests
- No JSON schema for resume file
- No validation of XPAK metadata structure
- No VDB CONTENTS format validation

### 3. API Contract Tests
- No tests verifying CLI output format stability
- No tests for `--json` output schema
- No eix/emerge compatibility verification

### 4. CLI/Route Tests
- No tests for `cmd/arise/main.go` (1564 lines untested)
- Flag parsing not tested
- Command dispatch not tested
- Error message format not tested

### 5. Atomicity Tests
- No tests for partial failure rollback
- No tests for `--resume` state consistency
- No tests for concurrent operations

### 6. Adversarial Input Tests (Partial)
Present in: `atom`, `depstring`, `ebuild`, `rebuild`
Missing in: `resolve`, `graph`, `merge`, `binpkg`, `phase`, `fetch`

## Test Infrastructure

### Makefile Targets
```make
test            # All tests
test-v          # Verbose
test-unit       # Excludes Property/Mutation
test-adversarial # Runs Adversar|Mutation tests
test-mutation   # Runs Mutation tests
test-race       # Race detector
test-integration # Requires Gentoo tree
test-coverage   # Coverage report
```

### Benchmarks
`internal/benchmark/benchmark_test.go`:
- `BenchmarkWalkCache` - md5-cache walking
- `BenchmarkResolve` - dependency resolution
- `BenchmarkSearch` - package search
- `TestCompare` - comparison tests

## Recommendations

### High Priority
1. **Fix data race** in `rebuild_test.go:436` - use mutex or channel
2. **Add sync tests** with git/rsync mocking
3. **Add CLI tests** for main.go command handlers
4. **Add property tests** for atom parsing, version comparison

### Medium Priority
5. **Add integration test CI** with minimal Gentoo tree
6. **Add schema validation** for resume file, XPAK, VDB
7. **Add adversarial tests** for resolver, graph, merge
8. **Add atomicity tests** for partial failure scenarios

### Low Priority
9. **Add API contract tests** for CLI JSON output
10. **Add mutation testing** with `go-mutesting` or similar
11. **Add benchmark CI** to track performance regressions

## Test Data

`testdata/` contains:
- `md5-cache/` - Sample cache entries
- `vdb/` - Sample VDB entries
- `corrupted/` - Malformed test data

Good foundation for adversarial tests.