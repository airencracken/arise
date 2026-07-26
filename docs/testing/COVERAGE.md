# Coverage tracking

Arise records periodic, local coverage snapshots in `docs/evidence`. Coverage
collection is intentionally not connected to hosted CI. A snapshot identifies
the commit and Go version so results can be reproduced and compared without a
paid reporting service.

## Measurements

Run the maintained core lane:

```sh
make test-coverage
```

This instruments all production packages but does not execute tests from the
benchmark, binary-package, fetch or live-integration packages. The command
writes `/tmp/arise-coverage.out` and
`/tmp/arise-coverage-functions.txt`. Use `make test-coverage-html` for an HTML
source view.

Package-local percentages answer a different question: how thoroughly does
each package's own test suite exercise that package? Collect them with:

```sh
go test ./cmd/... ./internal/... -cover -count=1 -timeout=60s
```

Do not compare a package-local percentage with the whole-tree core percentage.
Cross-package tests contribute to the latter, while excluded test packages can
still contribute to the former.

## 2026-07-25 baseline

The core lane covers 70.7% of statements, an increase of 2.3 percentage points
from the 2026-07-17 baseline. The machine-readable snapshot is
[`COVERAGE_BASELINE_2026-07-25.json`](../evidence/COVERAGE_BASELINE_2026-07-25.json).

The highest-value package-local gaps are:

| Area | Coverage | Best next test work |
|---|---:|---|
| `cmd/arise` | 29.4% | Route-existence and CLI contract tests around command dispatch; injected I/O and subprocess boundaries for command handlers |
| `internal/rebuild` | 64.9% | Mutation tests for lifecycle policy, adversarial ebuild input, and atomicity tests for failed phase/merge transitions |
| `internal/walker` | 65.3% | Property tests over generated trees, symlink cycles, permission failures, cancellation, and path traversal inputs |
| `internal/lifecycletrace` | 67.9% | Decoder fuzz/property tests for truncated, oversized, reordered, and unknown trace records |
| `internal/ingest` | 70.0% | Property tests for reconcile idempotence and atomicity tests for cancellation or malformed metadata mid-stream |
| `internal/merge` | 74.7% | Mutation and atomicity tests around collision, rollback, config protection, links, xattrs, and hostile filesystem layouts |
| `internal/executor` | 74.9% | State-machine mutation tests and cancellation/failure injection across concurrent actions |
| `internal/phaseproto` | 74.9% | Protocol contract and adversarial framing tests, including partial reads and invalid lengths |
| `internal/journal` | 75.1% | Crash-point mutation tests asserting rollback idempotence and durable state transitions |

The first diversity pass raised `internal/oplock` from 63.3% to 73.3% by
asserting path contracts, parent/file creation, nil-safe and idempotent release,
and reacquisition. It raised `internal/snapshotstore` from 76.4% to 82.7% with
failed-publication atomicity, cleanup, seed isolation, invalid retention and
retention property tests.

The walker pass raised `internal/walker` from 65.3% to 93.1%. More importantly,
it replaced ambiguous no-panic/log-only checks with exact contracts for nested
paths, symlinked directories, long paths, merged channels, repository policy,
overlay priority and mutable-slice isolation. Tightening the nested-path
assertion exposed and fixed an error where repository artifacts below
`category/package-version` depth were indexed as cache records.

The journal pass raised `internal/journal` from 75.1% to 76.6% while keeping
whole-tree coverage at 70.4%. It moved persisted-entry validation ahead of
recovery mutation: unknown statuses and kinds, duplicate or escaping paths,
and missing, misplaced or escaping backups now fail when the journal opens.
Atomicity tests prove a corrupt active journal cannot partially restore target
paths, and mutation-style assertions preserve the first captured preimage
across repeated capture calls.

The executor pass raised `internal/executor` from 74.9% to 76.0% and whole-tree
coverage to 70.5%. Concurrent runners must now provide exactly one successful
transaction-commit notification before their dependents are released or their
work is reported as successful. Tests reject missing and duplicate commit
proof, unproved post-commit errors, duplicate action identities, repeated
prerequisites and cycles that remain after independent work completes.

The ingestion pass raised `internal/ingest` from 70.0% to 78.8% while
whole-tree coverage remained 70.5%. Progress callbacks now correspond exactly
to durable batch boundaries: empty ingestion emits nothing, and exact batch
multiples are not reported twice. Property tests require parallel queries to
match serial database order for multiple worker counts and preserve callback
stop semantics. An atomicity test corrupts a stored record and proves the
parallel path decodes the complete snapshot before exposing any callbacks.

The lifecycle-trace pass raised `internal/lifecycletrace` from 67.9% to 75.2%
and whole-tree coverage to 70.6%. The decoder now validates short `openat2`
structures and requires journalable descriptor and shared-mapping targets to
be absolute. Adversarial tests cover empty and NUL paths, reader and resolver
failures, negative descriptors, pipes, deleted files, regular files,
directories and invalid ptrace reads. Ptrace-enabled integration and race
lanes verify the prototype behavior separately from sandboxed decoder tests.

The sync pass raised `internal/sync` from 68.4% to 83.8%. Local-repository
integration tests cover clone, update, detached heads, cancellation and
repository validation. Rename and copy histories now have exact ebuild-change
contracts: renames remove the old CPV and add the new CPV, while copies add
only the destination.

The install-lifecycle pass raised `internal/phaseproto` from 74.9% to 75.1%
and whole-tree coverage to 70.7%. PostgreSQL-shaped contract tests require
generated standard input to work with OpenRC helpers and require helper
failures to remain fatal even when later ebuild commands succeed. Custom
configure prefixes must derive the same ABI-specific library layout as
Portage. Merge atomicity tests require staged metadata to repair pre-existing
Redis-shaped config, data and log directories.

The mutation-guided pass raised whole-tree coverage to 70.8%,
`internal/journal` to 77.4%, and `internal/log` from 0% to 100%. The logging
tests assert default and runtime level transitions, structured attributes,
error-return contracts and adversarial odd argument lists. Journal survivors
produced new public-API tests for absent-tree coverage boundaries and nested
relative-symlink capture followed by rollback. A targeted `confined` run kills
60 of 107 covered mutants (56.1% covered-code score); many survivors alter
defensive `filepath.Rel` error branches that valid, canonicalized paths cannot
reach, so this result remains diagnostic rather than a quality gate.

The next survivor-driven pass covered the previously untested `PreflightAll`
audit API and killed all 25 targeted mutants. It requires a complete,
deterministically ordered failure set for malformed actions and rejects every
untrusted plan shape without mutation. Frozen action-to-rebuild configuration
now has field-completeness, map-isolation and live-eligibility matrix tests,
killing 41 of 42 targeted mutants (97.6%). Package-local executor coverage
rose to 82.2%.

Config-protection tests now exhaust the complete `._cfg0000_` through
`._cfg9999_` namespace, exercise deterministic filesystem errors, normalize
protect/mask boundaries and compare binary content. They kill 32 of 34
allocation/equality mutants (94.1%). CONTENTS parsing gained exact records,
malformed-leading input, path confinement and a native fuzz target; a
five-second local run executed more than 200,000 inputs without a failure.
Replacement and CONTENTS root conversion kill all 32 targeted mutants,
including parent-directory and prefix-sibling escape mutations. Package-local
merge coverage rose to 75.5%, and the core whole-tree lane now covers 71.0% of
statements.

Zero-percent command `main` packages are lower priority than the command logic
in `cmd/arise`: thin process entry points should be covered through route and
contract tests, not tests that duplicate Go's flag and exit behavior.
`internal/integration` is also expected to read as zero in the hermetic lane
because its value comes from explicitly tagged live comparisons.

## Snapshot rules

A new baseline must:

1. Run from a clean commit and record its full SHA.
2. Record the exact Go version.
3. Preserve the lane exclusions and instrumentation scope, or introduce a new
   lane instead of silently changing the denominator.
4. Include package-local measurements from the same commit.
5. Keep percentages in the inclusive range 0 through 100.
6. Explain regressions and prioritize tests by risk, not percentage alone.

Coverage is diagnostic evidence, not a release gate. A higher percentage does
not compensate for weak assertions; mutation, property, adversarial, contract,
integration and atomicity tests should be selected according to the behavior
being protected.

## Test-type maturity

`make test-mutation` runs tests whose names contain `Mutation`. Those tests
mutate input bytes or state and remain useful adversarial regression tests.
They are distinct from the real source-mutation lane:

```sh
go install github.com/jonbaldie/go-mutesting/v2/cmd/go-mutesting@v2.7.9
make test-mutation-analysis
make test-mutation-analysis MUTATION_TARGETS=internal/journal \
  MUTATION_MATCH=coveredByAbsentTree
```

The default target is the fast `internal/log` pilot. It killed 16 of 17
mutants (94.1%) on 2026-07-25; the lone survivor removes an explicit
`LevelInfo` initialization that is equivalent to `slog.LevelVar`'s zero value.
The first `coveredByAbsentTree` journal run killed only 4 of 10 covered
mutants. Tests derived from the survivors now exercise unrelated earlier
entries, path-prefix siblings, ordinary directories and rollback of a
descendant covered by an absent-tree record. They kill 18 of 23 mutants
(78.3%); the remaining survivors principally mutate defensive conditions that
cannot be reached after `confined` has canonicalized a public input.

Keep this lane local and scoped with `MUTATION_TARGETS` and `MUTATION_MATCH`.
The full transaction packages contain thousands of candidate mutations and
can take hours. Review surviving mutants before setting a threshold: record
equivalent and unreachable mutants separately from actionable escapes. Do not
publish a repository-wide mutation score until timeout behavior and equivalent
mutant review are stable.

The repository already has broad named adversarial coverage across parsers,
configuration, graph, phase, merge, rebuild, resolver, sync and walker code.
The next step is to make the checks systematic:

- Property/fuzz: parsers and decoders should never panic; successful
  parse/serialize round trips should preserve meaning; generated dependency
  graphs and filesystem trees should terminate within bounded resources.
- Atomicity: inject one failure at every durable write or rename boundary and
  assert either the complete old state or complete new state remains, never a
  mixture.
- API/route contracts: enumerate every CLI route, validate required arguments,
  exit classes and JSON schemas, and ensure malformed inputs never reach a
  mutating dependency.
- Integration: use local Git remotes, synthetic repositories and disposable
  roots for behavior that mocks would weaken. Keep live Portage comparisons in
  their existing opt-in lane.
- Mutation analysis: use surviving production-code mutants to identify weak
  assertions after the higher-risk invariants above are in place.
