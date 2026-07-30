# Arise Punch List

This is the ordered delivery plan for making Arise a safe, fast, real
competitor to `emerge`. P-numbers are stable milestone identifiers, not a
topological ordering; the dependency graph below governs execution order.

Complete feature parity and decisive performance are joint requirements.
Correctness work must preserve the indexed, immutable, concurrent architecture
that makes large speedups possible. Performance work may never weaken
compatibility, determinism, safety, or recovery.

A primary damaged-world acceptance criterion is eliminating manual repair by
successively selecting larger `--oneshot --nodeps` package subsets. Arise must
derive the complete repair closure, preserve safe ordering, verify the final
installed state, and explain every rebuild/removal instead of requiring the
operator to reconstruct the graph by trial and error.

## Ecosystem interoperability principle

Arise may enrich Gentoo's ecosystem, but must not capture it. Prefer durable
artifacts that common tools can inspect and use directly: ordinary files, text,
JSON, tar archives, VDB metadata, `emerge.log` records, hashes and detached
signatures. Arise-specific structured formats are acceptable when they provide
material safety or fidelity, but their schemas must be public and versioned and
they must emit a documented, useful compatibility projection wherever existing
tools serve the same user workflow. That projection may omit Arise-only detail,
but must preserve the external purpose—for example conventional merge timing
events for genlop/qlop/emlop alongside richer phase and transaction evidence.

Internal indexes, databases and caches must be rebuildable from authoritative
portable state rather than becoming the only copy of irreplaceable evidence.
For every new durable format, ask whether a user can inspect, validate, export,
recover or continue using its useful contents without the full Arise program.
If common tooling cannot reasonably understand it, provide a small standalone
reader/export path and a degraded open equivalent where one is meaningful.
Interoperability with Portage, eix, equery and the `q` tools is a product
contract, not an incidental side effect.

## Milestone dependency graph

```text
P0A performance evidence ───────────────────────────────────────────────┐
P0B fail-closed safety ──┬──> P4 execution ABI ──┬──> P6 transaction ─┼──> P8 product execution
                         │                       │                     │
P1 package state ──> P2 configuration ──> P3 resolver verifier ───────┘
                         │              │
                         └──> P5 verified fetch ─┘

P3 + P4 ──> P7 scheduler; production scheduling also requires P6
P5 + P6 + P8 ──> P9 binary-package integration
P10 product/tooling work draws on stable APIs from completed lower milestones
```

Important internal dependencies:

- P3 portable snapshots precede closing remaining live resolver differences.
- P3 whole-state verification precedes every executable build/merge plan.
- P4 protocol preflight and environment precede helper/lifecycle coverage.
- P5 Manifest planning and locks precede resume and mirror/RESTRICT policy.
- P6 journaling precedes any live-root merge, unmerge, world or VDB mutation.
- P7 can be tested synthetically now, but cannot schedule live mutations before P6.

The original P0--P8 dependency path has reached journaled live install,
reinstall, upgrade, removal, resume, and bounded parallel execution. Remaining
work is parity breadth and promotion rather than connecting those layers for
the first time. Near-term consolidation follows
[`docs/planning/CONSOLIDATION_CYCLE_PLAN.md`](docs/planning/CONSOLIDATION_CYCLE_PLAN.md):
ship a useful offline-build overlay, capture a frozen performance baseline and
accept one equivalence-gated optimization, implement world check/fix, then run
the reproducible fresh-stage3 G1 gate.

Status markers:

- `[ ]` not started
- `[~]` partial or under active work
- `[x]` acceptance criteria satisfied
- `[!]` release blocker

Resolver observability is captured by `support/perf/profile-p3-matrix.sh`: CPU, heap,
cumulative allocation, and Go execution traces are collected for Arise.
Optional `perf` and `strace` paths use executable operation probes and skip
cleanly when absent, permission-disabled, or unsupported. `--probe-only`
records capabilities without running a package-manager operation, while
`--syscalls` explicitly opts into the additional syscall-summary runs.

The 2026-07-18 expanded matrix exposed an empty-tree replay pathology: an
unverified plan consumed 219 seconds and approximately 85 GB of cumulative
allocations while incorrectly exiting successfully. Immutable candidate USE
maps and package/version keys are now cached, and impossible direct world or
system targets stop non-causal replay. The same live empty-tree case now
returns a structured nonzero failure in about 8.4 seconds, preserves all 19
reported constraints, and consumes one actual backtrack under a limit of 20.

The same evidence uncovered two live-world correctness bugs: repository-qualified
ROOT removals were discarded as though their repository separator were a domain
prefix, and authoritative empty dependency metadata fell back to package-wide
edges from a different version (injecting Oniguruma-9999 autotools dependencies
into Oniguruma-6.9.10). With both fixed, the aligned live `@world` pretend plan
is conflict-free and whole-state verified in about 3.3 seconds. Backtrack limits
20 and 10,000 produce identical normalized actions, removals, warnings, and
decision history while both consume one actual backtrack.

## Invariants (not completion tasks)

- A parsed flag is not an implemented feature.
- Every mutating feature has pretend, failure, interruption, and recovery tests.
- Filesystem mutations require a journal or an atomic replacement strategy.
- Repository/VDB/profile snapshots are immutable during one resolution.
- Concurrent output and results remain deterministic.
- Every parity claim links to a differential or end-to-end test.
- Performance work includes correctness checks and benchmark baselines.
- Every milestone has a same-snapshot comparison against Portage.
- No milestone ships on parity alone; it must meet its performance gate.
- No benchmark is valid unless result equivalence is asserted.
- Equivalent-but-slower than emerge or eix is a blocking bug. Those real
  workloads require at least 1.0x median speedup, and milestone budgets should
  be significantly higher wherever the architecture provides leverage.
- Portage-utils comparisons are aspirational and always published, but do
  not block builds solely for being below 1.0x.
- Unsupported behavior fails explicitly; it must never silently no-op.
- A copied static Arise binary works from standard Gentoo repository,
  profile, VDB and `/etc/portage` state without Arise-specific configuration.

## P0A — performance laboratory and budgets

- [x] Build a reproducible benchmark harness that runs Arise and Portage on the
  same immutable repository/profile/VDB snapshot.
- [~] Record hardware, kernel, filesystem, storage, Go, Python and reference-tool versions.
- [~] Separate cold-cache, warm-cache and incremental-operation measurements.
  The live deep/newuse world workload now has alternating warm and root-only
  cold-cache lanes; incremental-operation coverage remains.
- [x] Measure wall time, CPU time, process-tree peak RSS/PSS/USS, bytes
  read/written and process count. The harness records process-tree memory,
  peak descendant count, Linux storage read/write bytes, block I/O, and
  command CPU/wall time in every sample.
- [x] Store machine-readable benchmark results and equivalence verdicts.
- [~] Establish representative small, medium and full-world workloads.
- [x] Publish a full emerge/eix comparison matrix covering current and planned tasks.
- [x] Add statistical repetitions, median and p95 reporting.
- [ ] Define regression budgets before optimizing each subsystem.
- [ ] Reduce deep/newuse world-plan peak private memory without surrendering
  correctness or the latency lead. The 2026-07-25 baseline is 912.02 MiB warm
  and 939.07 MiB cold USS, versus emerge at 237.88 and 237.90 MiB. Profile
  allocation ownership, then remove duplicated immutable snapshot, candidate
  and graph state before considering persistent caches. The first accepted C2
  optimization reuses the state-fingerprint copy buffer: on the 2026-07-27
  live workload it reduced profiled allocation by 53.3%, combined CPU time by
  33.9%, median wall time by 8.4%, and median peak RSS by 2.3%. Retained graph
  and decoded-snapshot state remains the next-cycle memory target. A separately
  authorized follow-up cached normalized implicit USE_EXPAND prefixes, improving
  median wall time another 8.5% and CPU time 10.2%; allocation profiles fell
  17.2% total and 27.5% retained, although noisy process peak RSS rose 8.7%.
  Aggressive follow-up tuning stopped loading VDB `CONTENTS` payloads into the
  resolver graph and streamed dependency metadata into graph edges. Interleaved
  evidence reduced median wall time to 2.73 seconds and median peak RSS to
  610.5 MiB. A final package-policy match cache was rejected after doubling CPU
  and regressing wall time to 5.26 seconds.
- [ ] Fail performance CI on material regressions beyond the agreed noise band.
- [ ] Keep microbenchmarks, but gate releases on end-to-end workloads.
- [~] Track test coverage in separate deterministic-core, network/integration
  and benchmark-harness lanes. Whole-tree instrumentation gives the fast core
  lane a 68.4% statement baseline while counting excluded production code as
  uncovered. The benchmark lane passes in 119.4 seconds, covers 66.5% of its
  harness package and exercises 13.5% of the whole production tree. The default
  `go test ./...` lane is now hermetic: live Portage comparisons require the
  explicit `live_portage` build tag, and binhost HTTP tests inject a transport
  instead of requiring a sandbox-forbidden loopback listener. Tagged host
  commands have measured class-specific deadlines, isolated process groups,
  descendant cleanup and bounded waits. Initial live measurements distinguish
  `portageq best_visible` at 0.44s, `equery belongs` at 14.45--14.69s, deep
  `@system` at 34.2s and standard `@world` at 56.1s. The earlier apparent
  `equery` hang was ten legitimate expensive repetitions, not a stuck process.
  Read-only emerge comparison/performance lanes preserve the caller's FEATURES
  and append `-news`, avoiding sandbox-blocked post-plan news ownership changes
  without changing dependency resolution; a live probe completes with status
  zero under this supported Portage feature gate.
  Real socket coverage remains an opt-in external
  lane. Safety-critical thresholds remain to define after per-file risk
  classification.
- [x] Expose resolver phase timings for candidate search, complete-graph
  expansion, post-solve verification and sorting in verbose output.
- [x] Add a real forced-backtracking scaling benchmark at 20, 100 and 1,000
  failed decisions. Initial baseline: 0.59 ms/251 KiB, 10.1 ms/2.8 MiB and
  706 ms/216.6 MiB respectively, exposing quadratic full-map snapshots.
- [x] Replace full resolver branch snapshots with nested transactional undo
  logs. At 1,000 forced decisions this reduced 706 ms/216.6 MiB to
  109 ms/9.76 MiB (6.5x faster, 22.2x less allocation) while preserving the
  live 185-action/4-conflict/backtrack-1 plan. A faster experimental CP/slot
  index reached 28.6 ms but changed Docutils constraint semantics and was
  rejected; canonical indexed slot state remains future work.

- [x] Cache normalized `USE_EXPAND_IMPLICIT` prefixes once per resolver instead
  of lowercasing and allocating them for every effective candidate flag.
  Same-state live deep/newuse evidence preserves exact plan/state digests while
  reducing median wall time from 3.16 to 2.89 seconds and median CPU time from
  5.10 to 4.58 seconds.
- [x] Keep VDB `CONTENTS` out of resolver-only installed-state snapshots while
  still requiring it as committed-record evidence. Full VDB/ownership callers
  retain payload access. Interleaved live runs reduced median wall time from
  2.91 to 2.82 seconds and peak RSS from 933.6 to 607.2 MiB; aggregate CPU rose
  10.0%, an explicitly accepted speed-first tradeoff.
- [x] Stream dependency metadata directly into graph edges rather than
  recursively allocating metadata slices and a second edge-pair list. Exact
  plan/state digests are unchanged; interleaved median wall time improved from
  2.80 to 2.73 seconds and CPU from 5.06 to 4.94 seconds.

Initial benchmark operations:

- exact and substring search, installed listing and ownership lookup;
- initial full index and one-sync incremental index;
- single-package, multi-package and `@world` pretend plans;
- plan rerun with one package.use change;
- fetch-only with cached and uncached distfiles;
- independent synthetic builds and a dependency-shaped build DAG;
- resume after a mid-graph failure;
- depclean calculation and preserved-rebuild scan.

Acceptance gate:

- [ ] The harness detects intentionally introduced result mismatches and
  performance regressions, and produces reproducible reports on this laptop.

## P0B — stop unsafe or misleading behavior

- [x] Gate mutation commands until their transaction paths are independently
  ready. Install, reinstall, update, exact uninstall, deselect, and bounded
  multi-action source plans now cross preflight, state-bound approval, operation
  locking, journaling, recovery, and final verification. Depclean, prune,
  preserved/reverse-dependency rebuild execution, binary-only acquisition, and
  audit fixes remain explicitly gated while their corresponding complete plans
  and transaction paths are unfinished.
- [x] Remove or qualify “full emerge parity” claims in user documentation.
- [x] Convert unsupported ebuild phases and FEATURES from silent success to
  explicit errors. Unknown phases and unsupported lifecycle entry points return
  typed execution-ABI errors. The production phase worker discovers exported
  and default phases, implements real default `src_prepare`/`eapply_user`, and
  rejects enabled known or unknown features outside its supported set before
  mutation.
- [x] Fix all current `go vet` findings; keep vet clean in CI.
- [x] Add a schema version and application version to persistent state.
- [x] Keep progress indicators ASCII-only so early boot, recovery consoles and
  systems without Braille-capable fonts remain readable.
- [x] Add a global operation lock compatible with Portage's live-system safety
  expectations. Arise uses Portage's exact VDB directory-lock path and POSIX
  record-lock namespace; subprocess fixtures prove contention and release.
  Mutation entry points remain gated until they can hold it for transactions.

Acceptance gate:

- Commands cannot report success for work they did not perform.
- Documentation accurately distinguishes usable, experimental, and planned behavior.

Current live safety evidence includes successful journaled install, reinstall,
upgrade, exact removal, deselect, multi-action execution, lifecycle, and resume
operations. Pretend and failed preflight paths retain zero-mutation fingerprint
tests, while incomplete maintenance mutations continue to fail explicitly.

## P1 — correct package-state model

### Repository metadata

- [x] Replace CP-only records with immutable repository+CPV records.
- [x] Preserve repository name, path, priority, masters, EAPI and overlay order.
- [x] Add secondary indexes for CP, slot, repository and visibility inputs.
- [x] Make concurrent ingestion deterministic regardless of goroutine ordering.
- [x] Support incremental sync/index transactions and stale-record removal.
- [x] Publish atomically cloned repositories with the parent repository
  directory's read/traverse visibility, repair repositories created by older
  arise versions before updating them, and require `profiles/repo_name` to
  match the configured repository name before reporting sync success. Identity
  mismatches never publish clone staging directories.
- [x] Detect md5-cache changes using digest/mtime without trusting them as package state.
- [x] Index repositories without a pre-generated metadata/md5-cache, marking
  statically discovered records incomplete and unsafe for resolution.
- [ ] Generate authoritative metadata for uncached overlay ebuilds in an
  isolated EAPI/eclass-aware metadata phase, atomically replace their
  incomplete discovery records, and permit resolution only after the generated
  record passes the same validation as repository md5-cache input. Until then,
  overlays intended for Arise execution must publish metadata/md5-cache.

### Installed state

- [x] Create a separate VDB ingestion model.
- [x] Preserve installed CPV, slot/subslot, repository, USE, IUSE, dependencies,
  build time, build ID, COUNTER, EAPI and CONTENTS.
- [x] Support multiple installed slots and versions.
- [x] Reconcile installed state with filesystem truth on every read; no
  persistent installed cache is trusted.

### Tests

- [x] Property test: ingestion order cannot change indexed results.
- [x] Fixture: multiple versions of one CP all survive indexing.
- [x] Fixture: same CPV in multiple repositories follows repository priority.
- [x] Fixture: repository records are never marked installed without VDB evidence.
- [x] Fixture: multiple installed slots survive production graph construction.
- [x] Fixture: unknown future EAPI and metadata keys survive indexing.
- [x] Crash test: interrupted index leaves the previous snapshot readable.

Acceptance gate:

- [x] A state dump lists exactly the same available CPVs and installed CPVs as
  Portage for the test machine and fixture repositories.

Current live differential: available repository records match 36,111/36,111
and installed CPVs match 1,233/1,233. This includes repository multiplicity and
local-overlay CPVs without md5-cache entries.

## P2 — Portage configuration and profile evaluation

- [x] Wire the active profile into production state construction.
- [x] Implement profile parents and repository masters deterministically. Local
  and cross-repository profile graphs handle cycles, diamonds and traversal;
  repos.conf plus layout.conf masters order repository policy master-first with
  explicit missing-master and cycle failures.
- [x] Implement make.defaults stacking and variable expansion needed by package policy.
  Declarative make.globals, multiline quoted assignments, root-to-leaf profile
  layers, `${VAR}` expansion, incremental tombstones and user overrides match
  Portage's live policy-variable values.
- [x] Implement USE_ORDER. The supported environment, package, configuration,
  defaults, package-internal, feature, repository and env.d layers reduce to
  the same active value and effective-USE results as Portage.
- [x] Implement USE_EXPAND, USE_EXPAND_HIDDEN and implicit expansion flags.
  Global expansion agrees with Portage, implicit flags remain package-local,
  and the 100-visible-CPV effective-USE corpus is exact.
- [x] Match modern Portage's source-install BDEPEND default (`auto`) instead of
  silently defaulting to `n`; explicit `n` continues to override auto mode.
- [x] Sort production plans from the exact selected version's dependency
  expressions instead of package-level fallback edges; place PDEPEND after its
  parent and assert dependency ordering before exposing an executable plan.
- [x] Make plan ordering slot-, repository-, USE-conditional-, and any-of-aware
  so parallel slots and inactive dependency branches cannot create false edges.
- [x] Implement use.mask/use.force and stable/package variants. Global,
  stable-only and full-atom package policy honor parent removals and match the
  100-visible-CPV Portage effective-USE corpus exactly.
- [x] Implement package.use matching using full Gentoo atoms, ordering and removal syntax.
  User and profile rules preserve file/profile order; wildcard, version, slot
  and repository-qualified atoms agree with the live effective-USE corpus.
- [x] Implement package.mask/unmask and repository/profile masks with reasons.
  Version, slot and repository-qualified administrator masks now filter
  candidates; master-ordered cross-repository and active-profile stacks honor
  removal syntax, and user unmask restores matching candidates. Structured
  source/reason provenance matches Portage's GLEP 84 output on the live corpus.
- [x] Implement ACCEPT_KEYWORDS and package.accept_keywords accurately. Ordered
  full-atom rules, removal syntax, wildcard keywords and empty-rule `~ARCH`
  shorthand select the same best-visible CPV as Portage across the live corpus.
- [x] Implement license groups, expressions, ACCEPT_LICENSE and package.license.
  Repository/profile group stacks expand nested and negative groups with cycle
  safety; ordered full-atom package rules and boolean/USE-conditional LICENSE
  evaluation preserve later overrides.
- [x] Implement package.env and supported per-package environment layering.
  Ordered full-atom rules compose referenced env files with variable expansion,
  incremental USE/FEATURES semantics and traversal safety. Package execution
  reconstructs effective global configuration before one-shot overrides, then
  reduces base -> package.env -> retained command environment -> explicit
  request overrides deterministically. The P4 builder injects that final
  environment and rejects shell-startup injection plus protocol-owned work,
  image, root, scratch and home variables before worker startup. Connecting
  resolved actions to this completed handoff belongs to P4.
- [x] Match Portage's command-environment configuration overlays and precedence
  for temporary invocations (including `USE`, `FEATURES`, `ACCEPT_KEYWORDS`,
  `ACCEPT_LICENSE`, config/root/repository selectors, build controls and output
  controls). Cover both direct execution and privilege-boundary behavior with
  differential tests so a copied Arise binary honors the same one-shot settings
  as `emerge` without persisting them. Direct execution now uses an explicit
  allowlist for incremental policy, toolchain/build controls, root/config/repo
  selectors, storage and output controls; CLI flags override environment-backed
  defaults and the rebuild worker consumes the same effective configuration.
  Direct, filtered-root and fixed explicit-root probe bundles now establish the
  privilege boundary: the normal user/root effective environment hashes match,
  explicit post-transition values change the expected command layer, and Arise
  deliberately cannot reconstruct values removed by `su`/sudo. Evidence and
  snapshot fingerprints are recorded in
  `docs/evidence/P2_PARITY_EVIDENCE_2026-07-17.json`.
- [x] Implement package.provided with version-aware matching. Active profile
  stacks and `/etc/portage/profile` overrides honor ordered removals, and only
  compatible provided versions satisfy resolver constraints.
- [x] Build @system from the active profile. Root-to-leaf profile stacking,
  starred membership, removals and version/slot/repository-qualified atom
  expansion feed both `@system` and the system portion of `@world`.

### Tests

- [x] Differential test effective USE against Portage for at least 100 real CPVs.
  The deterministic visible corpus uses Portage's evaluated `PORTAGE_USE` and
  currently matches 100/100 CPVs with non-empty IUSE on this laptop.
- [x] Differential test visibility/best-visible and masking reason. Best-visible
  matches Portage for 100/100 multi-version package names, and normalized GLEP
  84 reason text matches 25/25 masked CPVs.
- [x] Matrix tests for profile parent precedence and package.* directories. Parent
  chains, diamonds, cycles, ordered package.use overlap and policy removal have
  fixtures; directory-form use, keywords, license, mask/unmask, provided and env
  families load in lexical order with their production representations checked.
- [x] Regression fixture for ABI_X86 and `abi_x86_32?` dependencies. Atom
  USE defaults and conditional/equality operators round-trip and resolve
  against parent USE; the live `net-im/signal-desktop-bin` full-plan comparator
  now matches Portage exactly at 1/1 action with no normalized differences.
- [x] Regression fixture for IUSE `+flag` and `-flag` defaults.
- [x] Property test that configuration reduction is deterministic.

Acceptance gate:

- Arise agrees with Portage on visible candidates and effective USE for the
  selected real-world corpus.

Immutable evidence manifest (2026-07-17): the enabled global USE set
matches `portageq envvar USE` exactly at 231/231 flags and the deterministic
package-specific effective-USE corpus matches 100/100 visible CPVs. Portable
policy/precedence tests plus the fingerprinted 100-USE, 100-visibility, 25-mask,
nine-policy-variable and privilege-boundary results close the P2 gate. Raw host
bundles remain local because they contain machine policy and paths.

## P3 — dependency expression and resolver correctness

### Reusable solver-library architecture

- [ ] Extract the generic decision engine behind a pure-Go library boundary,
  initially inside this repository, while Arise retains Gentoo atom, USE,
  slot/subslot, blocker, profile, repository, dependency-domain and rebuild
  semantics as its frontend.
- [ ] Make rule and decision provenance first-class so callers can ask why a
  candidate was selected/rejected and receive structured conflict sets and
  decision/backtrack traces.
- [ ] Audit libsolv's compiled-rule, dense-ID, learned-conflict, transaction and
  problem-reporting designs as implementation references only. Do not add a
  libsolv or cgo dependency; any future direct backend must be optional,
  USE-gated, and retain the complete native static fallback.
- [ ] Raid Paludis/cave, Exherbo/exheres, and historical Funtoo Portage/ego for
  resolver-library boundaries, decision explanations, annotated dependencies,
  provider/slot/alternative models, cross-domain semantics, repository kits and
  discarded experiments. Track adopted and rejected ideas with primary-source
  provenance and Gentoo parity fixtures.
- [ ] Prove the library boundary against existing portable resolver fixtures,
  representative Portage parity matrices and a deep/newuse world-update corpus
  before exporting a stable API or splitting it into another module.

Detailed boundary and migration plan:
[`docs/planning/SOLVER_LIBRARY_PLAN.md`](docs/planning/SOLVER_LIBRARY_PLAN.md).

### Live plan-equivalence campaign

The potentially superior live plan is tracked as a hypothesis, with threats to
validity and promotion criteria, in
[`docs/evidence/PLAN_COMPLETENESS_VALIDATION.md`](docs/evidence/PLAN_COMPLETENESS_VALIDATION.md).

- [x] Add normalized Arise/emerge pretend-plan parsers and structured action-set
  differences preserving CPV, slot/subslot, repository, action and USE groups.
- [x] Add a command that runs equivalent Arise and emerge plans and reports
  only-in-Arise, only-in-emerge, version, location and action differences.
  The comparator independently models `--deep`, `--newuse`, `--with-bdeps`,
  `--complete-graph` and backtrack limits so `-uDN` gates cannot accidentally
  compare different command semantics.
- [x] Make that comparator consume Arise's versioned JSON plan rather than
  color- and wording-sensitive terminal output.
- [x] Classify comparator outcomes before timing: equivalent verified plans,
  verified Arise repair versus unresolved Portage partial plan, unverified Arise
  versus resolved Portage, and other non-equivalence are distinct machine-readable
  classes. Portage stderr conflict diagnostics are retained for classification
  without polluting action parsing.
- [x] Normalize emerge USE/USE_EXPAND groups into canonical flags and fail plan
  equivalence when any Portage-reported effective flag differs. Hidden
  Arise-only profile flags remain visible in JSON without causing false gates.
- [x] Match Portage's plain explicit-atom behavior: `install cat/pkg` selects a
  newer visible version without requiring `--update`; dependencies and expanded
  sets retain their normal update/deep controls.
- [~] Compare `@world --complete-graph` and explain every extra/missing action.
  The first live normalized comparison reduced 320 noisy textual differences
  to 90 real differences, then to 47 after slot/action fixes, to 5 after
  scoping set completion to the selected dependency closure, and to 4 after
  matching dependency constraints against every installed slot. The remaining
  Portage plan is itself partial due to slot conflicts, so exact comparison
  needs a conflict-free snapshot as well as this intentionally broken laptop.
- [~] Gate the representative set-update workflows independently: deep/newuse
  `@system`, standard `emerge -uDN @world`, and the recovery-heavy
  `--keep-going --with-bdeps=y --complete-graph --backtrack=1000 @world` form.
  Permanent correctness-gated workload definitions now exist for all three;
  no performance result is publishable until its normalized plan is equivalent.
- [ ] Fix candidate-selection and reinstall-classification differences,
  prioritizing interpreter transitions, live packages and changed dependencies.
- [~] Produce structured installed-versus-replacement slot-conflict causes.
  Slot conflicts now retain machine-readable package, slot, atom and dependency
  reason records with transactional rollback. Post-solve dependency, blocker,
  parse and repair failures now also emit typed verification details, including
  the affected retained package where known, and JSON plans preserve them.
  Installed/replacement candidate state and complete rendering remain.
- [~] Differentially cover `@system`, single-package installs, `--with-bdeps`,
  `--deep`, `--complete-graph`, and high-backtrack plans independently. Shallow
  `@system` now matches exactly at 11/11 actions with effective USE equality,
  and the Signal single-package plan matches 1/1. The comparator exposes
  independent deep and with-bdeps controls; Signal remains exact with
  `--with-bdeps=y`, `--with-bdeps=n`, complete graph, and backtrack 1,000.
  Fixing deep installed-dependency promotion moved the non-newuse diagnostic
  from 13/131 to 129/131 actions. The exact deep/newuse gate now reaches a
  structured 165/142 comparison instead of aborting. Separating installed IUSE
  declarations from implicit effective USE removed 42 false Arise reinstalls;
  default dynamic-deps traversal then recovered the current ebuild dependency
  on `acct-user/root` that was absent from historical VDB metadata. The remaining
  corrected source-plan `--with-bdeps=auto` traversal recovered six Perl
  virtual updates, and associative nested-any-of handling removed a false
  s6/OpenRC branch. Preserving ordered all-of tuples inside any-of alternatives
  then aligned the Mesa Python/Mako/Packaging/PyYAML/Cython implementation
  branch. Final-state dependency traversal now replaces stale installed-parent
  metadata with an already scheduled parent version, removing Python 3.13 and
  its obsolete Portage dependency chain. Applying `--newuse` throughout the
  traversed deep dependency closure then recovered nine of fourteen missing
  Portage rebuilds and reduced the normalized difference count from 36 to 32.
  Ordered profile force/mask reduction now lets package-level negative mask
  entries cancel global and stable masks without overriding unrelated masks,
  eliminating the remaining shared-action USE mismatch. Version/slot-scoped
  complete-graph verification also stopped one selected Python slot from
  repairing unrelated installed Python 3.9/3.10 slots and their obsolete
  ensurepip dependency. Subslot-aware package policy matching then stopped a
  `:3/5.0` mask from hiding the valid Eigen `:3/3.4` rebuild. Deep update/newuse
  policy now propagates through the installed any-of choice,
  matching Portage's `dep_zapdeps` `want_update` behavior and recovering the
  Vala and elinks actions. Verification repair now tries an ordinary matching
  candidate before requiring a mutable USE repair, recovering vcs-versioning.
  A single bounded refresh of direct updates belonging to exact version/slot
  entries that survived transactional branch rollback then recovered the final
  JSON-PP action without rescanning the installed graph. The remaining
  23-only-Arise/0-only-emerge identities are concentrated in
  Python/Perl transitions, changed dependencies, alternative-provider preference and
  conflict-driven partial set closure; all shared actions have exact CPV,
  classification and effective-USE parity. Provenance collapses the 23 extras
  into three roots rather than independent unexplained actions: the Portage ->
  gemato -> requests/mypy transition closure, the Git -> Perl module/virtual
  closure, and one Qt blocker replacement. Portage's displayed plan is partial
  because its unresolved Python and Qt slot conflicts suppress those roots.
  No Portage action is missing. Exact action-set equality now requires a
  conflict-free frozen snapshot or a deliberate partial-plan comparison model,
  not count-driven deletion of valid Arise closure.
- [x] Compare conflicted live plans at backtrack 20, 100 and 1,000 before
  attributing plan-completeness differences to either resolver. Portage
  remained at 135 actions and the same four-action gap at every limit; Arise
  produced 139 actions and needed one backtrack decision.
- [~] Promote the normalized comparisons into permanent same-snapshot fixtures
  and correctness gates, including effective USE equality. Historical
  installed-only `(-flag%)` output is excluded from the replacement USE domain
  by a permanent parser regression; live shallow @system and Signal gates still
  need a sanitized immutable snapshot before they are machine-independent.
  A versioned read-only reference harness now captures command/stdout/stderr/
  status records from emerge, portageq, equery, portage-utils and eix in smoke,
  standard, full and explicit privilege-boundary lanes. Snapshot fingerprinting,
  sanitization and promotion of captured bundles remain. The package-state
  schema now captures every repository and installed-VDB field required for
  resolver replay, normalizes host repository paths, embeds a deterministic
  SHA-256 identity, rejects tampering/unknown fields/incompatible schemas, and
  reconstructs the resolver's repository and VDB records. Reviewing private
  repository/policy data and promoting the current laptop corpus still remain.

- [~] Preserve complete per-version dependency expressions in the graph. The
  compact resolver snapshot and VersionInfo retain all five dependency classes
  per version; candidate-specific traversal and nested any-of/all-of tuples are
  covered by regression tests. Package-field validation now rejects
  REQUIRED_USE-only cardinality operators and empty groups rather than
  flattening them into a different plan. Broader live nested-choice coverage
  remains.
- [~] Complete EAPI-correct DEPEND/RDEPEND/BDEPEND/IDEPEND/PDEPEND parity.
  Resolver records preserve EAPI; BDEPEND before EAPI 7 and IDEPEND before
  EAPI 8 are ignored as unavailable metadata variables, matching Portage,
  while malformed syntax in active classes still fails closed. Retained
  packages keep RDEPEND/PDEPEND, never IDEPEND,
  and include DEPEND/BDEPEND only according to `--with-bdeps`. Source versus
  binary transaction roots are covered by portable fixtures; broader live
  Portage differential coverage remains.
  Cross-root placement now follows the historical boundary: pre-EAPI-7 DEPEND
  is satisfied in the running BROOT, while EAPI 7+ DEPEND uses SYSROOT;
  BDEPEND/IDEPEND use BROOT and runtime/post dependencies use ROOT.
  `--root-deps=rdeps` omits legacy pre-EAPI-7 DEPEND, while
  `--root-deps=True` additionally satisfies DEPEND/BDEPEND/IDEPEND in ROOT.
  Repository-scoped `eapis-banned` and `eapis-deprecated` policy is ingested
  from layout.conf and retained in resolver snapshots: banned candidates are
  ineligible, deprecated candidates remain eligible with a warning, and
  installed historical records remain available for recovery. The current
  Gentoo policy bans EAPIs 0-6 and deprecates EAPI 7. Package
  dependency grammar is EAPI-aware for strong blockers (EAPI 2), slot deps
  (EAPI 1), slot operators (EAPI 5), repository qualifiers, USE dependencies
  and USE defaults, while unknown future EAPIs remain forward-compatible.
  A single declarative EAPI 0-9 contract table now gates dependency syntax,
  IUSE defaults, REQUIRED_USE, dependency-class activation, DEPEND root domains
  and inactive any-of behavior; adding an EAPI requires one capability row.
  Raw IUSE is retained in live graph records and portable resolver fixtures so
  historical metadata validation survives capture and replay.
  EAPI 7's empty-any-of transition is preserved: an any-of whose every option
  is disabled by an inner USE conditional is satisfied through EAPI 6 and
  unsatisfied in EAPI 7+, while a conditional enclosing the whole group still
  disables it. Group and option conditions remain distinct in compact metadata.
  Operation-mode reduction now matches Portage for shallow/deep
  `--buildpkgonly`, target-scoped `--onlydeps-with-rdeps` and
  `--onlydeps-with-ideps`, including a fix for versioned action keys that could
  leave the explicit target in an `--onlydeps` plan. A portable transaction-root
  matrix now covers EAPI 6 source, EAPI 8 source, EAPI 8 binary automatic-bdeps
  and EAPI 8 binary explicit-bdeps plans across every dependency class; the
  full live Portage differential matrix remains.
- [x] Implement complete atom semantics, slots, subslots and repository constraints.
  Repository-qualified targets and dependencies now reject candidates from the
  wrong repository, including installed candidates. Identical CPVs from
  multiple repositories now remain distinct resolver candidates, unqualified
  selection uses repository priority, and `::repo` can select a shadowed copy.
  Numeric version components now retain PMS significance, matching Portage's
  ordering of `1.0 < 1.0.0` instead of normalizing trailing zero components.
  The live differential corpus now passes atom round trips, version ordering,
  dependency satisfaction, positive/negated USE conditionals, repository/VDB
  metadata, visibility, effective USE, effective policy variables and 25
  package-mask reasons. Current Portage metadata command signatures and full
  slot/subslot identities are preserved by the oracle rather than generating
  shifted fields or false subslot-mask failures.
  Package dependency fields reject repository-qualified atoms for supported
  EAPIs even though repository-qualified user targets remain valid. The `~`
  operator matches an exact base version while ignoring only `-rN`, rather than
  wrongly admitting longer numeric versions. The atom boundary rejects blocker syntax,
  empty repositories and USE members, unterminated/trailing input, embedded
  control bytes and malformed standalone slot/subslot operators instead of
  normalizing invalid input into a different atom. Round-trip fuzzing found and
  permanently captured the slot and NUL truncation cases. Version comparison
  now also preserves leading-zero fractional components, treats omitted suffix
  numbers and revisions as zero, accepts valid lowercase letter suffixes and
  rejects unknown suffix keywords, matching live Portage oracle cases that
  previously changed candidate ordering. Operator atoms now require a version,
  and version wildcards are accepted only with `=`. Category, slot/subslot and
  repository boundary characters follow Portage's ASCII forms: dotted
  categories, dotted/`+` slots and hyphenated repositories are preserved, while
  empty or invalid-leading slot components, dotted repositories and operators
  with no version fail instead of being normalized or accepted. Named `slot*`
  and `slot/subslot*` forms are rejected while standalone `:*` remains valid.
  Package dependency fields reject versioned atoms without an operator even
  though internal action identities retain CPV parsing. Contradictory duplicate
  USE dependencies and invalid disabled conditional/equality forms fail at the
  atom boundary. Version comparison now consumes Portage's installed `vercmp`
  corpus and compares arbitrary-precision numeric components, suffixes and
  revisions without machine-integer overflow.
  Portage's non-wildcard `test_atom` and `test_isvalidatom` syntax corpus is
  retained as a Go regression table. Package names with a version-like suffix
  are rejected without incorrectly rejecting valid doubled-hyphen CPVs.
  Package-constraint parsing is distinct from internal repository/VDB CPV
  parsing, so bare versioned targets and set entries fail instead of silently
  ignoring their version. Equal-glob matching uses Portage's normalized literal
  prefix and version-part boundary semantics, explicit subslots participate in
  satisfaction, version constraints reject versionless candidates, and missing
  IUSE flags require an explicit USE-dependency default. Repository, slot,
  subslot, version and USE constraints therefore all participate in candidate
  matching while full CPV identities remain available internally.
- [x] Implement USE dependency defaults and conditional forms. Candidate
  satisfaction now has a full truth-table corpus for `flag?`, `!flag?`,
  `flag=`, `!flag=`, `flag(+)` and `flag(-)`, and repository/VDB graph records
  preserve EAPI so pre-EAPI-2 USE dependencies and pre-EAPI-4 defaults are
  rejected rather than silently misinterpreted.
- [x] Implement REQUIRED_USE as a real boolean/cardinality expression evaluator,
  including all-of, any-of, exactly-one, at-most-one and USE conditionals.
- [~] Implement blockers and replacement/unmerge ordering. Versioned blockers,
  target/world conflicts, coordinated replacement upgrades and planned
  uninstalls have resolver fixtures. Weak `!` versus strong `!!` semantics now
  remain distinct through dependency edges, and strong planned removals carry
  an explicit remove-before-merge requirement. Blocker matching now enumerates
  every matching installed version deterministically: qualified blockers retain
  unrelated parallel slots, unqualified blockers schedule every matching slot,
  and removal actions preserve exact version/slot/repository identity. Correct
  explicit-subslot matching exposed and closed an installed-identity leak in
  replacement selection: qtbase's soft blocker on
  `<dev-qt/qt5compat-6.11.1:6` had constrained replacement lookup to the old
  subslot and converted five coordinated Qt updates into removals. Replacement
  lookup now uses a CP-only constraint and tests require
  install-without-uninstall; the live plan is restored to all five coordinated
  updates. Enforcing remove-before-merge ordering belongs to the P4 transaction
  scheduler and remains a live-mutation gate.
- [x] Verify the complete planned installed state before execution. Ordinary
  resolution and explicit install/removal overlays share one fail-closed gate;
  general uninstall, reverse-dependency breakage, repaired replacement,
  parallel-slot retention and invalid-action matrices are permanent tests.
  Immutable ROOT, SYSROOT and BROOT installed views are checked according to
  DEPEND/RDEPEND/BDEPEND/IDEPEND/PDEPEND domains, including any-of, providers
  and blockers. Planned actions and removals carry their destination domain,
  retain distinct identity and ordering across roots, and serialize that domain
  in JSON; aliased native roots collapse to one action. The broader emerge
  source/binary cross-root differential matrix remains under EAPI semantics.
- [~] Implement virtual/provider selection and provider preference. Provider
  atoms now enforce version, slot and USE constraints and provider alternatives
  use bounded transactional rollback. Modern repository virtuals are ordinary
  `virtual/*` packages and exercise the any-of path; the separate legacy
  PROVIDE map is currently synthetic and no PROVIDE records exist in this live
  repository or VDB. Historical PROVIDE ingestion/EAPI semantics and real-world
  virtual preference parity still need differential coverage. The live Perl
  5.42.0-r1 -> 5.42.2 transition deliberately exposes a stricter result:
  Portage displays only Perl and leaves installed `virtual/perl-parent-0.241`
  with a false Perl-5.40 dependency, while Arise complete-graph repair adds
  `virtual/perl-parent-0.244` and verifies the final state. A permanent fixture
  proves non-complete mode fails verification and complete mode produces the
  verified two-action repair; this documented improvement is not removed for
  superficial action-count parity.
- [~] Implement any-of groups with installed and minimal-change preferences.
  Group identity, installed preference, conditional USE dependencies and
  unavailable-alternative filtering are implemented. Installed preference now
  searches every installed slot rather than only the numerically highest
  instance. Alternatives are now
  attempted transactionally with complete search-state rollback and bounded
  backtrack accounting. Mutable USE repairs inside an any-of group are previewed
  without leaking state and committed only for the chosen branch; this unblocks
  dependency-required alternatives. Nested any-of groups now retain one
  associative group identity while preserving per-option USE conditions, so an
  installed OpenRC outer alternative wins over an unnecessary s6 USE repair.
  Explicit all-of tuples inside any-of groups are preserved and planned as one
  transactional alternative; their declared implementation order outranks
  stale installed-tuple preference while singleton alternatives retain installed
  provider preference. Context-specific validation now matches Portage by
  rejecting `^^`, `??` and empty groups in package dependency fields instead of
  silently flattening REQUIRED_USE-only cardinality operators; those operators
  remain fully evaluated in REQUIRED_USE. Broader real-package alternative and
  provider preference differentials still gate general grouped-dependency parity.
- [x] Implement circular dependency detection and useful diagnostics. Active
  dependency paths emit one deterministic cycle chain without recursing to a
  depth-limit failure or rejecting an otherwise resolvable plan.
- [ ] Canonicalize and deduplicate circular-dependency diagnostics across
  complete-graph passes, verifier repairs and equivalent entry points into the
  same SCC. The 2026-07-21 continuation plan was verified with zero conflicts
  but emitted 503 warnings, 486 of them repeated cycle chains rooted in the
  Perl/libcrypt/glibc/toolchain component. Emit one stable representative per
  canonical SCC/cycle signature with an occurrence count and compact provenance
  for distinct triggering edges. Preserve full machine-readable evidence on
  demand without making saved plans enormous or repeatedly formatting the same
  chain. Add fixtures proving deterministic ordering, rotation-independent
  identity, duplicate suppression across resolver passes, and retention of
  genuinely distinct cycles within one SCC. Measure allocations, bytes and
  resolution time before and after; warning construction must not dominate a
  successful plan.
- [x] Implement slot conflict explanations. Machine-readable causes retain each
  parent requirement and enumerate installed/available candidates with
  visibility plus per-constraint satisfaction and rejection verdicts.
- [~] Implement subslot rebuilds and complete-graph behavior. Deterministic
  direct/transitive slot-operator rebuilds, unchanged/no-operator cases, set
  scoping and ignore-built-slot-operator-deps have fixtures; live reverse-VDB
  propagation still differs from Portage.
- [x] Implement changed-use, newuse and changed-deps from installed metadata.
  Effective USE changes are distinct from IUSE-domain additions, and changed
  dependencies compare installed VDB metadata across all five dependency classes.
- [~] Implement real backtracking with bounded decision history. Version
  revision, provider fallback and any-of fallback now consume one shared budget
  and emit a deterministic machine-readable decision ledger. Ledger entries
  commit and roll back with speculative transactions, preserving the invariant
  that history length equals used budget. A later accumulated-constraint or
  whole-state failure can now rewind an earlier locally successful any-of
  decision, replay resolution with the next deterministic alternative and
  record the `conflict-rewind` edge in plan JSON for debugging/visualization.
  Earlier provider and repository-available version choices are replayable as
  well; installed-only historical versions are deliberately not treated as
  downgrade candidates. With multiple remaining alternatives and `Jobs > 1`,
  isolated replay attempts run concurrently and the earliest successful
  preference wins independent of completion order. Plan JSON records every
  evaluated branch, outcome, conflicts and local backtrack use for future
  tables, timelines and graph visualizations. Broader nested decision search,
  cancellation and adaptive speculative scheduling remain.
- [~] Version constraints are now accumulated per CP/slot, enforce one final
  candidate per slot, and consume the configured backtrack budget when a later
  constraint revises an earlier version choice. Any-of choices now use full
  branch snapshots. Provider alternatives now also use transactional exploration
  with installed preference. Conflict replay now evaluates multiple remaining
  any-of/provider/version branches concurrently from isolated resolver state
  and commits in deterministic preference order, never completion order.
  General nested speculation and performance tuning remain.
- [ ] Produce structured conflict causes for autounmask and human output.
- [~] Plan dependency-required USE changes without silently applying them.
  Direct mutable USE requirements now retain the candidate, affect hypothetical
  verification, and emit an actionable blocking package.use change. Profile
  masks and force rules remain authoritative. `--autounmask-write` now writes
  idempotent package.use entries for both file- and directory-style user
  configuration. Complete-graph verification now feeds late dependencies back
  into the solver and aggregates their transitive USE requirements instead of
  leaving false post-solve conflicts. Complete-graph replacements use current
  effective USE rather than leaking flags from the installed instance; full
  Portage autounmask parity remains.
- [x] Stop forcing `Deep` for ordinary installs; new targets always evaluate
  dependencies while already-satisfied installed packages only recurse under `--deep`.
- [x] Keep installed-only VDB records distinct from repository candidates.
  Removed ebuilds can no longer be selected or scheduled as fictional rebuilds;
  `GetBestVersion` now enforces its available-version invariant.
- [x] Collapse invalid retained dependencies of an installed-only package into
  one actionable depclean-candidate diagnostic instead of emitting one conflict
  per obsolete dependency. Installed-only packages outside the selected
  world/system closure are warnings rather than blockers; explicitly retained
  broken packages still block. Update operations never silently depclean.
- [x] Make complete-graph traversal, final slot enumeration and action-map
  conversion deterministic. Repeated solves now assert identical actions,
  conflicts and backtrack counts.
- [~] Model source and binary merge dependency classes separately. Binary
  candidates omit `DEPEND`/`BDEPEND` under normal `--usepkg` automatic policy
  while retaining `RDEPEND`/`IDEPEND`/`PDEPEND`; explicit `--with-bdeps=y`
  restores built-package build dependencies exactly as Portage does. A
  2026-07-18 live rerun after repository/configuration drift currently compares
  159 Arise actions with 143 Portage actions and 16 normalized differences;
  these are fixture candidates and make new timing results ineligible. One shared
  bdeps reducer now prevents installed, source, binary and edge traversal from
  interpreting `auto` differently. `--usepkgonly` fails when no usable binary
  exists, including under `--nodeps`. GPKG metadata and remote-binhost candidate
  discovery remain under P9.
- [~] Tag every dependency edge with its PMS filesystem domain: `BDEPEND` and
  `IDEPEND` use `BROOT`, `DEPEND` uses `SYSROOT`, and runtime/post dependencies
  use `ROOT`. Separate installed-state views and cross-root solving remain.

### Tests

- [~] Differential plan tests against `emerge -p` on a curated corpus. The
  aligned live explicit-package matrix is exact for Links (1/1), jq update
  (1/1), Gource (5/5), ripgrep (1/1), Emacs (5/5) and Neovim (16/16), including
  Ruby (44/44), normalized action, CPV, slot/subslot, repository, merge type
  and effective USE. The 73-action total spans minimal, update, C/C++, Rust,
  editor and multi-slot language-runtime shapes. Promotion into immutable
  machine-independent fixtures remains; Python transitions remain a dedicated
  higher-complexity corpus rather than being hidden inside one world result.
  The next pretend-only expansion covers silver-searcher, ack, xterm,
  rxvt-unicode, st/st-terminfo, screen, tmux, BusyBox and genkernel for C/Perl,
  terminal/terminfo coupling, session/PAM policy, multicall userland and kernel
  tooling behavior. That ten-case expansion now passes 10/10 on the live
  comparator: silver-searcher 1/1, ack 2/2, xterm 2/2, tmux 1/1 and six verified
  current/no-op cases, for 6/6 normalized actions overall. The dated record is
  `docs/evidence/P3_EXPLICIT_CORPUS_EXPANSION_2026-07-19.json`; immutable
  machine-independent promotion remains.
- [x] Live configuration/metadata semantic differential gate against current
  Portage. The complete tagged matrix passes atoms, version comparison,
  repository and VDB metadata, raw GCC ebuild/eclass inheritance, dependency
  satisfaction, USE conditionals, visibility, effective USE, environment policy
  and package-mask reason provenance. Resolver action-plan differentials remain
  the separate corpus item above.
- [~] Independently validate resolver plan impact without reusing candidate
  selection. A translation-only adapter freezes installed/available packages,
  effective USE, dependency metadata, ROOT/SYSROOT/BROOT state and actions.
  Direct verified package plans are audited after resolution and replayed under
  the operation lock. All dependency classes, USE dependencies/defaults,
  conditionals, REQUIRED_USE cardinality, slot operators and frozen
  mask/keyword/license/EAPI policy are covered. Identical inherited VDB defects
  are classified as pre-existing while request and action violations remain
  non-waivable. The live 11-action `games-misc/fortune-mod` pretend plan and the
  installed `dev-util/pkgcheck` no-op plan pass silently. Enforcement remains
  blocked on frozen set expansion, provider/action-order validation, complete
  profile-policy provenance and a clean classified corpus.
- [~] Differential plans now compare source versus binary merge intent from
  Arise JSON and Portage's `[ebuild]`/`[binary]` output. Expand the corpus across
  `-k`, `-K`, `--binpkg-respect-use`, source fallback and cross-root cases.
- [~] Capture the development laptop's current broken-world resolver state as a
  sanitized fixture, including removed ebuilds, stale Python targets, virtual
  transitions, blockers and overlays, before repairing the live installation.
  A strict reusable JSON fixture format now preserves separate installed and
  available dependency/USE metadata without host paths. The first portable
  stale-Python-target fixture reproduces `--newuse` current-versus-installed
  dependency drift, an installed-only removed leaf and complete-graph repair.
  The format now preserves repository priority and has deterministic normalized
  encoding plus CP/provider reduction tooling. Encoding rejects absolute host
  paths and control data; ordering mutations produce byte-identical output, and
  reductions retain only explicitly selected version/provider records for
  replay validation. Fixtures carry normalized Arise and Portage expectations;
  the stale-Python transition records Arise's verified two-action repair and
  Portage's unverified one-action partial plan as distinct outcomes.
  The aligned live recovery command and its five installed Python slots are
  recorded in `docs/evidence/P3_BROKEN_WORLD_BASELINE_2026-07-18.json`; reducing
  the wider reverse-dependency fanout, blockers and overlays remains.
- [x] Repair the formerly failing laptop `--update @world` differential.
  Installed/repository EAPI contamination and raw live-LLVM candidate selection
  are fixed, and deep/newuse set planning now produces comparable actions.
  With the caller's exact recovery semantics (`--deep --newuse
  --complete-graph --with-bdeps=y --keep-going --backtrack=20`), Portage
  completes dependency resolution in 47.39 seconds using 3/20 backtracks;
  Arise exceeded 120 seconds without emitting a plan and was terminated. This
  aligned scaling failure is now part of the P3 baseline rather than being
  conflated with the earlier non-equivalent probe.
  A fresh 2026-07-20 comparison after the live Python/preserved-library cleanup
  now resolves and verifies on both sides. Follow-up fixes made installed VDB
  `:=` edges authoritative for reverse rebuilds, preserved explicit IUSE
  defaults across duplicate cache declarations, allowed an unqualified updated
  dependency to escape an already-scheduled old slot, and handled ordered Rust
  toolchain alternatives without globally abandoning installed preference.
  The final Python omission was a provenance edge: replaying a planned parent
  could retract a same-version dependency rebuild after that dependency was
  marked seen. A final pass scoped strictly to direct dependencies of planned
  parents restores those `--newuse` actions without scanning unrelated selected
  CPs or installed slots. The current live gate is exact: 369 Arise actions,
  369 Portage actions, both verified/resolved, and zero normalized differences.
  After removing the obsolete standalone `perl-core/Compress-Raw-Zlib-2.213.0`
  generation with its installed lifecycle hooks, a fresh repository-state gate
  again resolves a verified 369-install, zero-removal plan. All 369 actions pass
  the read-only whole-plan preflight. Circular-dependency diagnostics remain in
  JSON/saved plans but are shown in normal terminal output only with `--verbose`,
  keeping large-plan summaries usable without discarding evidence. A fresh
  independent Portage comparison on the same state is exact at 369/369, both
  verified/resolved, with zero normalized differences.
- [~] Resolver execution is wall-clock bounded through a context-aware API.
  `ResolveContext` cooperatively cancels target expansion, candidate search,
  complete-graph work, verification and speculative replay; the historical
  `Resolve` entry point remains as an unlimited compatibility wrapper. The CLI
  defaults `--resolver-timeout` to five minutes and accepts `0` to disable it.
  Cancellation returns a non-executable `incomplete` result with a structured
  cause, phase, elapsed time, decisions and actual backtracks, including in JSON
  output. Repeated five-minute failures are treated as a resolver scaling or
  design defect requiring profiling and root-cause reduction, not as acceptable
  pathological outcomes or grounds for increasing the default. JSON telemetry
  now records complete-graph passes, candidate evaluations, replay branches,
  verifier passes/repairs, undo-log operations, cancellation checkpoints,
  allocations and allocated bytes. Cancellation checks occur inside candidate
  and whole-state verifier scans as well as phase boundaries; a 1,000-version
  regression proves a deadline stops without exhausting the candidate set.
  Identical single-constraint and accumulated slot-constraint candidate searches
  now reuse deterministic positive or negative results. A monotonic generation
  invalidates the cache on every committed or rolled-back USE override, so
  speculative repair cannot reuse stale effective-USE decisions; JSON reports
  cache hits and misses for the live scaling profile.
  The first five-minute CPU profile isolated the scaling defect: 433 million
  allocations (25.6 GB) drove sustained GC scanning while effective USE policy
  repeatedly reparsed the same CPVs and package.* atoms during committed direct-
  update refresh. Package-policy atom parsing now uses a concurrency-safe
  positive/negative cache, wildcard rules bypass candidate parsing, and parent
  effective USE is cached per resolver node. Direct-update refresh has its own
  timed phase and cannot downgrade context cancellation under `--keep-going`.
  The optimized live rerun completed in 25 seconds with 8.26 million
  allocations/627 MB, versus 301 seconds and 433 million allocations/25.6 GB.
  It also exposed a replay-boundary bug that reported 21/20 backtracks: an
  exhausted internal zero budget was being mistaken for the public zero-value
  default of ten. Replay attempts now preserve exact zero and a regression
  enforces the hard ceiling. `support/perf/profile-p3-matrix.sh` expands the same root-only,
  pretend-only CPU/perf/cProfile capture to independently selectable world,
  system, explicit-package, preserved-rebuild and empty-tree cases, building
  Arise once and retaining per-case commands, exits, timings and profiles.
  The first matrix run found a shared cycle-ordering defect (87 `@system` and
  110 explicit-package false conflicts); deterministic SCC condensation now
  orders the component DAG and accepts only unavoidable intra-component order.
  Local replays are verified with zero conflicts in 2.61 seconds for `@system`
  and 7.22 seconds for the explicit three-package case. Preserved-rebuild fell
  from 258 seconds to 8 seconds by replacing per-ELF `ldd` subprocesses with a
  native `DT_NEEDED`/loader-directory scanner. Live preserved discovery now
  reads Portage's JSON preserved-libraries registry instead of classifying all
  versioned SONAMEs as preserved; owners are not incorrectly rebuilt merely for
  supplying an old object. A later warm empty-set comparison measured Arise at
  1.477 seconds versus Portage at 6.133 seconds; an exact CMake reinstall plan
  measured 1.535 seconds versus 3.250 seconds. Defer a separate incremental
  Badger linkage index until repeated non-empty profiles show that native ELF
  discovery is again a leading cost. If admitted, cache per-CPV owned ELF,
  ABI/SONAME, `DT_NEEDED`, and RUNPATH facts—not the transient rebuild answer—
  with VDB-bound invalidation and atomic generation publication. CPU profiles
  finalize on SIGINT/SIGTERM so bounded
  pathological captures remain usable. Empty-tree no longer performs the
  installed-parent refresh or verifier mutation loop; its remaining closure
  differences are emitted within a one-backtrack diagnostic run instead of
  allocating 33 GB inside repeated verification repairs. Replay now records
  canonical visited override states and discards child overrides when rewinding
  a parent. Alternatives already impossible under committed slot constraints
  are excluded before consuming a backtrack. A live explicit jq deep/newuse
  diagnostic that previously cycled through unrelated Perl/Rust/sandbox choices
  for 19--24 seconds and exhausted 20 backtracks now reaches the authoritative
  non-complete-graph verification boundary in 3.76 seconds with zero replay
  decisions, preserving all 13 structured damaged-world causes. Verification
  failures that require reverse-dependency repair are terminal when
  complete-graph repair is disabled.
- [x] Golden resolver tests cover weak, strong and versioned blockers;
  exact parallel-slot removals and subslot-operator rebuilds; constrained,
  missing and backtracking virtual providers; and installed preference,
  unavailable alternatives, tuple preservation, nested branches and
  whole-state rollback for any-of groups.
- [x] Property tests for topological ordering and solution satisfaction.
  Linear, selected-version, slot/repository/USE-conditional/any-of and resolved
  result ordering matrices assert dependency-before-parent placement. The
  solution property overlays selected versions and checks ordinary plus any-of
  constraints, while the mandatory verifier matrices cover retained packages,
  parallel slots, removals, blockers, USE and ROOT/SYSROOT/BROOT domains.
- [x] Add a mandatory post-solve whole-state verifier before permitting a real
  transaction: overlay the proposed installs/removals on the VDB snapshot and
  prove every retained and planned dependency, blocker, slot and USE constraint.
  Resolve results now carry an explicit verified/failed/skipped-nodeps/incomplete
  status, JSON plans expose it, and the production non-pretend boundary rejects
  every result not marked verified. Pretend remains available for diagnostics;
  `--nodeps` cannot silently cross into execution. Dependency domains, general
  removal overlays and planned-action root placement are enforced; portable
  world parity remains a separate resolver-corpus gate.
- [x] Post-solve verifier now overlays multiple installed slots, preserves
  candidate versus installed USE/dependency metadata, and validates affected
  retained reverse constraints. Exact per-version planned removals no longer
  erase unrelated parallel slots from the overlaid state, and removals now join
  installs in the affected-name set so retained consumers are revalidated.
  Regression matrices cover a failed replacement, a complete-graph repair and a
  removal that breaks a retained runtime dependency; their verification status
  and structured conflict records are asserted. The captured shallow `@system`
  fixture remains an exact conflict-free 11-package plan. A current five-run
  rerun is exact at 11/11 and measures 1.43s versus 6.48s (4.52x); the current
  deep/newuse live state is separately
  open and must not inherit that claim.
  The damaged world transition now reaches whole-state verification; its stale
  interpreter targets and ordering failures must become portable fixtures before
  further repair/backtracking.
- [x] Mutation tests that remove or invert constraints and must fail. A verified
  planned-install baseline is independently mutated across version, slot, USE
  and blocker constraints; every mutation fails whole-state verification and
  retains a structured post-solve conflict.
- [x] Fuzz atom and dependency expression parsers with round-trip invariants.
  Atom and dependency-expression round-trip/idempotence plus version-comparison
  antisymmetry fuzz targets run from permanent valid seeds; three minimized
  atom parser regressions remain in the normal corpus. Local 5-second runs
  completed 1.4 million atom, 1.3 million version and 1.3 million dependency
  expression executions after the discovered slot/NUL failures were fixed.
- [x] Permanent explicit-package regression on the laptop snapshot. Signal was
  installed before its uninstalled state could be frozen and is no longer a
  valid oracle. The replacement `www-client/links` case was captured while
  uninstalled. Portage and Arise now select exactly `www-client/links-2.30-r3`
  as one action, and the independent uninstalled `app-editors/neovim` case is
  exact at 16/16 actions. The installed jq update is exact at 1/1. These cases
  caught and permanently fixed non-deep traversal through already-satisfied
  BDEPEND tools, which had downgraded explicit roots and invented a false
  lzip/zlib-ng/Python closure during conflict replay.

Acceptance gate:

- Arise's candidate set, closure and action intent match emerge for the corpus,
  except for explicitly documented and tested improvements.

## P4G — Go supply chain

Overlay-owned module generation and offline packaging are tracked separately
from the core execution ABI; they do not block P4 closure.

### Overlay-owned Go dependency supply chain

- [x] Ship the initial release-bound offline dependency archive path. The
  versioned overlay ebuild consumes a Manifest-verified module-cache archive
  tied to the release, so installation does not depend on a mutable Go proxy or
  a pre-populated global module cache. Per-module Gentoo packaging remains the
  experiment below rather than a prerequisite for the first release.
- [ ] Build a reusable `g-cpan`-style Go module packaging tool for Gentoo.
  Consume `go.mod`/`go.sum` and Go's structured module metadata; resolve module
  paths, semantic/pseudo versions, major-version suffixes, `replace`/`exclude`
  directives and transitive requirements; verify upstream sums and source
  archives; discover licenses with an explicit human-review queue; and emit
  deterministic reviewable ebuilds plus dependency sets for a selected overlay.
  Support audit, generate, update and divergence-check modes without silently
  editing unrelated overlay content. Keep its module-to-Gentoo model available
  as a standalone Go library so it can serve other Go projects, with Arise's
  dependency overlay as the first end-to-end corpus.
- [ ] Package every third-party Go module required by Arise in the overlay,
  pinned to the exact module version and source digest used by `go.sum`.
- [ ] Generate module-package ebuilds and the Arise dependency set from
  `go.mod`/`go.sum`; fail CI when the overlay and module graph diverge.
- [ ] Make the Arise ebuild build fully offline from overlay-managed module
  sources without committing `vendor/` or contacting a Go proxy.
- [ ] Verify that overlay packages populate an isolated module source/cache
  consumed explicitly by the ebuild, rather than relying on mutable global Go
  build artifacts.
- [ ] Record licenses, upstream source, checksums, transitive relationships and
  security-update ownership for every packaged module.
- [ ] Compare per-module packaging with the locked release dependency archive
  for reproducibility, maintenance burden, mirror behavior and Gentoo policy;
  retain a tested offline fallback during migration.
- [ ] Add an end-to-end empty-cache/no-network install test for the released
  Arise ebuild and all overlay-managed Go dependencies.

## P4 — real EAPI/ebuild execution ABI

- [~] Design a versioned Go-to-Bash execution protocol. Version 1 defines a
  validated run-phase request and line-delimited phase/log/QA/result events;
  request IDs, strict per-worker sequence numbers and one terminal exit status
  prevent output cross-talk under high job counts. A clean-environment Bash
  worker now completes the handshake, sources a selected ebuild, invokes one
  phase and returns ordered logs/results. The default backend now follows
  Portage by requiring its `sandbox` executable for filesystem mediation and
  failing closed when it is unavailable. An explicit enhanced Bubblewrap mode
  supplies private user, PID, IPC, UTS, cgroup and network namespaces, a minimal
  read-only runtime, private tmp/proc/dev and a single read-only ebuild bind;
  it never silently falls back. Portage-like direct network, IPC, mount and PID
  namespace assembly is capability-detected independently, warns and degrades
  to the remaining protections when unavailable, and never requires an
  unprivileged user namespace. Worker commands now run in an isolated process
  group; context cancellation sends TERM to the entire tree, escalates to KILL
  after a bounded grace period and bounds Wait even when a descendant inherited
  protocol pipes. A TERM-resistant descendant stress regression passes
  repeatedly without leaks. Cancellation is now returned as an ordered typed
  control/signal event and persisted with the terminal error in the durable
  package log, so interrupted work is distinguishable from a malformed worker
  stream. Command completion also requires exactly one
  terminal result: missing/duplicate results, trailing data and process/result
  status mismatches fail closed. Wiring FEATURES/RESTRICT policy and the complete
  environment contract remain. A poisoned-parent regression proves the worker
  receives no ambient host variables: only fixed protocol defaults and
  caller-authorized request variables cross the process boundary. The request can
  carry a typed Manifest-verified artifact set; the worker revalidates it before
  startup, injects DISTDIR, and enhanced isolation binds it read-only. The Bash
  runtime is maintained as an embedded `worker.sh`, so static recovery builds
  retain a single executable while the source remains directly syntax-checkable,
  ShellCheck-compatible and diffable against Portage's Bash runtime.
- [~] Source the chosen ebuild and inherited eclasses in an isolated environment.
  The selected ebuild is sourced in a no-profile/no-rc Bash process with a
  reserved environment allowlist and protocol-only stdout. An enhanced-mode
  fixture proves arbitrary host files outside explicit binds are invisible.
  Runtime inherit now resolves safe eclass names through deterministic
  caller-supplied repository/master directories, sources nested inheritance
  once, rejects cycles/missing eclasses and implements EXPORT_FUNCTIONS phase
  wrappers. Package policy now derives child-first eclass directories from the
  selected repository graph with missing/cycle preflight. Connecting resolved
  actions to that request builder and the full eclass helper environment remain.
  Legacy rebuild unpacking now receives exact Manifest-verified artifact names
  instead of scanning the shared DISTDIR and rejects selected unknown formats.
  Eclass inheritance, namespace policy and the complete controlled
  source/dist/work bind layout remain release blockers. A live Signal rehearsal
  sources its real ebuild and `pax-utils`, `unpacker` and `xdg` inheritance chain,
  then successfully executes its prepare and install phases against a disposable
  image tree without mutating the host. The stronger live gate now Manifest-
  verifies the real 110 MiB Signal `.deb`, executes its genuine unpack through
  `unpacker`/`multiprocessing`, prepare and install phases, and validates the
  resulting executable and launcher in 7.7 seconds.
- [~] Implement phase discovery and EAPI default phases. Discovery now sources
  the real ebuild/eclasses and emits deterministic ebuild-defined and exported
  phase events separately from the declared EAPI 7/8 default sequence. Requests
  reject other EAPIs before worker startup and reject a sourced EAPI mismatch.
  The versioned worker now supplies default src_prepare/eapply_user with exact
  Portage-compatible PN/P/P-PR and slot-qualified patch precedence, basename
  override/empty suppression, a controlled WORKDIR, an application tag and
  explicit patch status propagation. Package policy derives existing patch
  directories from package identity. Remaining default phase bodies and
  production migration from the legacy runner block deleting its synthetic
  success path.
- [~] Implement required environment variables and directory layout. Requests
  now own absolute WORKDIR, S, D and ED paths; enhanced isolation binds work,
  source and image directories read-write and prevents package.env from
  overriding them. ROOT, SYSROOT and BROOT are explicit independently validated
  domains; strict isolation maps non-host roots read-only without rebinding `/`.
  T/TMPDIR/TMP/TEMP and HOME are controlled writable scratch locations and are
  likewise reserved from environment injection. Package policy now derives a
  typed, revision-aware identity and the worker exposes protocol-owned CATEGORY,
  PN, PV, PR, P, PVR, PF, SLOT and PORTAGE_REPO_NAME; package.env cannot
  override them. Typed PORTAGE_BUILDDIR and PORTAGE_CONFIGROOT paths now flow
  through package policy and isolation, and phases distinguish Portage's
  `EBUILD_PHASE=compile` from `EBUILD_PHASE_FUNC=src_compile`. The normalized
  VDB environment snapshot retains the complete controlled path contract.
  The userpriv launcher preserves this curated environment across `runuser`;
  without that flag util-linux replaced the package-local HOME with
  `/var/lib/portage/home`, causing ordinary builds that initialize `.local` or
  `.cache` to fail for lack of permission.
  Remaining PMS variables outside the declared initial contract remain.
- [~] Implement a minimum complete helper ABI for current supported EAPIs.
  Explicit-status eapply/eapply_user, emake, econf, dodoc and einstalldocs now
  support the EAPI 7/8 default prepare/configure/compile/test/install path.
  Archive/image helpers now cover Signal's real unpack/install path, and the
  Portage-compatible `has` primitive supports its transitive eclasses. The core
  install family now includes into/insinto/exeinto, insopts/exeopts, binary and
  sbin installation/renaming, executable/data/header/library installation,
  directory/keepdir, renamed documentation and manual pages. Contract tests
  assert destinations and modes. Service/config helpers now cover init.d,
  conf.d and env.d installation/renaming; docinto and ownership changes share
  the same image contract. Every image destination,
  including permission and link helpers, rejects lexical traversal outside D.
  Directory option state now controls dodir/keepdir; the live Portage oracle
  caught that libopts and dohard are banned in both supported EAPIs and that
  newinfo is unavailable, so Arise rejects rather than emulates them and keeps
  static/shared library modes independent from insopts. Renamed
  header/library/manual helpers plus info-page
  installation have positive destination/mode tests and fail-closed argument
  matrices. A disposable live Portage differential now executes the same helper
  corpus for EAPI 7 and 8 and requires exact normalized image paths, object
  types, modes, contents and symlink targets. It permanently captures the EAPI
  8 header/conf.d/env.d option-state transition and Portage's empty-directory
  cleanup boundary. The worker now performs that post-src_install transition:
  EAPI 8 prunes unclaimed empty image directories, EAPI 7 retains them, and
  keepdir markers survive both. Common eclass primitives now include in_iuse,
  usev, use_with and use_enable with enabled/disabled, rename and value forms.
  Negated use queries match the effective USE set. Nonfatal preserves ordinary
  command status without leaking PORTAGE_NONFATAL, while die remains terminal;
  both the recoverable path and Portage's terminal boundary are permanent tests.
  The file-processing tranche is now explicit: dohtml, dosed, generic dolib,
  libopts and dohard reject as banned for EAPI 7/8; domo installs fixed-mode
  locale catalogs; docompress queues and exclusions drive deterministic
  gzip/bzip2/xz/zstd post-install compression using the typed Portage setting;
  and dostrip queues/exclusions drive policy-gated ELF stripping. Hermetic tests
  cover compression round trips, exclusions and stripped-versus-preserved ELF
  payloads, while the live EAPI 7/8 image differential exactly matches Portage
  bzip2 paths, bytes, translations and modes. Full option semantics and the
  remaining helper families remain blockers.
- [~] Execute pkg_setup, source phases, pkg_preinst/postinst and pkg_prerm/postrm.
  The protocol rebuild path now runs setup, every source phase, preinst, a
  disposable-root merge/VDB write and postinst in order. Undefined lifecycle
  functions use their EAPI no-op defaults, and unpack executes from WORKDIR
  rather than S. Replacement prerm/postrm ordering and transaction integration
  remain. Each phase now has a fresh worker and a saved-environment handoff;
  pkg_setup uses Portage's unsandboxed host-IPC/network policy, while
  pkg_preinst is separated from the build sandbox and installed lifecycle
  phases use the Portage network/IPC/PID table. pkg_pretend now executes during
  preflight. Old replacement removal-hook failures warn and continue rather
  than failing the newly committed replacement. clean/cleanrm, success/die
  hooks and lifecycle-write recovery remain release blockers; see
  `docs/audits/PORTAGE_EXECUTION_PARITY_AUDIT_2026-07-21.md` and the phase-edge
  follow-up `docs/audits/PORTAGE_PHASE_EDGE_PARITY_AUDIT_2026-07-22.md`.
- [~] Honor FEATURES, RESTRICT and PROPERTIES. Phase requests now carry a typed
  execution policy derived after package.env composition; USE-conditional
  RESTRICT/PROPERTIES expressions are evaluated before startup. Sandbox,
  network/IPC/PID isolation, userpriv, test, fetch, strip and interactive intent
  are explicit. Unsupported enabled features and properties fail before worker
  startup; they never silently degrade. userpriv and usersandbox are distinct,
  phase-scoped policies: namespace setup remains privileged and selected build
  phases enter through the portage account. Complete phase-specific behavior
  and the full real-root policy differential matrix remain. Current dogfood wrappers disable fixlafiles,
  multilib-strict, qa-unresolved-soname-deps, strict, userpriv and usersandbox,
  so those runs are not evidence of normal emerge parity. Versioned Portage
  install-QA directories are now discovered, fixlafiles rewriting is
  implemented, and core QA scripts execute. Real-root credentials,
  supplementary groups, real jobserver-token transport through the complete
  launcher chain, strict QA outcome differentials and phase-specific property
  exceptions remain hard gates.
- [~] Match Portage's durable build and elog logging contract before live
  mutation. Ordered phase/log/result events and one terminal status are already
  enforced. A fail-closed per-package log manager now reserves UTC timestamped
  ordinary and split-log paths, writes ordered sequence/job/phase/kind/stream
  records, syncs and finalizes them, atomically publishes optional gzip output,
  and maintains `T/build.log` as the canonical symlink. The typed phase request
  reserves and exposes an absolute `PORTAGE_LOG_FILE`; package.env cannot
  override it. Collision, ordering, gzip and duplicate-finalization regressions
  pass. Worker event ingestion is now wired end to end through the phase-protocol
  rebuild path: success events and terminal failures persist with durable paths
  in errors, finalization is mandatory, and parallel 100-record package streams
  prove no cross-package interleaving. `PORTAGE_LOG_FILTER_FILE_CMD` now uses
  Portage-like shell-word parsing and a streaming subprocess; filter startup,
  write or exit failures fail closed while preserving the durable path. Worker
  `einfo`, `elog`, `ewarn`, `eerror` and `eqawarn` calls produce typed INFO, LOG,
  WARN, ERROR and QA events. Class selection and the `echo`, timestamped `save`
  and locked `save-summary` sinks are wired through protocol rebuilds; requested
  mail, custom or unknown sinks reject before the first worker. Syslog delivery
  is implemented with visible connection/write failure. Exact Portage formatting,
  and the remaining failure-injection matrix remain. Every unfiltered record is
  synced and each in-progress package owns a durable active marker; successful
  finalization clears it only after log publication. Startup recovery can list
  path-confined interrupted logs without deleting evidence, and regressions
  prove partial output survives and finalized logs leave no stale marker.
  Complete
  timestamped per-package logs under effective `PORTAGE_LOGDIR`, the ordinary
  and `split-log` layouts, `compress-build-logs`, `T/build.log`, and a reserved
  `PORTAGE_LOG_FILE` visible to phases and QA checks. Preserve combined terminal
  ordering while retaining source stream, phase, package/job identity and
  monotonic sequence metadata; never interleave concurrent package logs.
  Support `PORTAGE_LOG_FILTER_FILE_CMD`, the standard elog severity classes and
  at least echo/save/save-summary/syslog-compatible delivery before claiming
  emerge logging parity. Keep the structured durable package log canonical,
  but append a locked and synced Portage-compatible transaction projection to
  `emerge.log` so genlop, qlop, emlop and similar tools can observe both active
  and completed Arise merges and unmerges. Compatibility output must never
  discard or flatten the richer Arise evidence, and its failure policy must be
  explicit and tested. Mail and custom command delivery may remain explicit
  opt-ins, but requested unsupported sinks must fail visibly. Log open/write/
  sync/finalize failures must fail closed before or during mutation rather than
  silently discarding the only recovery evidence. Separate worker stderr, QA
  producers, persistent transaction summaries and externally delivered signal
  events remain; context cancellation records are complete.
  Core logging is therefore near closure and is not a request to redesign the
  subsystem. The live-mutation blockers are production-executor hookup, exact
  Portage formatting/differentials, external signal records, and the
  remaining injected open/write/sync/filter/compress/rename/finalize failures.
- [x] Preserve the static Go control plane when Python is unavailable. The
  protocol and isolation launcher are Go and execute Bash directly without
  importing or invoking Portage Python. This is also an intentional planning
  distinction from Portage: Arise does not retain or specially order the
  package owning Portage's current Python interpreter merely to keep the
  package manager alive. Python dependency, slot, USE, ABI and final-state
  correctness remain mandatory; only the manager-survival special case is
  absent.
- [x] Declare supported EAPIs and reject unsupported EAPIs before mutation.
  The initial ABI declares EAPI 7 and 8; request validation happens before
  sandbox/namespace startup, and the sourced declaration must match preflight.

### Tests

- [x] Synthetic fixtures cover discovery, exported phases and the EAPI 8 default
  prepare/configure/compile/test/install pipeline. A complete EAPI 7/8 matrix
  executes every declared source/default and lifecycle phase, including default
  unpack and the no-op lifecycle defaults, through the real worker protocol.
- [~] Eclass inheritance and exported phase fixtures cover nested inheritance,
  missing eclasses, true cycles, shared-diamond dependencies and exported
  discovery/execution. Modern metadata-cache `_eclasses_` name/hash pairs now
  reconstruct complete transitive INHERITED state. Static raw-eclass closure
  handles conventional include guards, simple ebuild-variable conditionals and
  literal EAPI case selection; a live GCC 15.3.0 comparison matches Portage's
  full inherited set. Repository-master precedence and a broader representative
  real-eclass corpus remain. The live corpus now also includes Signal's complete
  transitive eclass chain and exact exported phase discovery.
- [~] Helper ABI contracts cover patch precedence/failure, Makefile defaults,
  image installation and default docs for the initial helper subset. The worker
  is a standalone `.sh` source checked by `bash -n` in the normal test gate;
  `make test-shellcheck` provides optional richer linting when installed. A
  zero-user-patch regression prevents empty associative-array lookups, and a
  disposable-root end-to-end test validates phase order, installed payload,
  lifecycle side effects and VDB creation through the versioned worker. The
  live-tag lane additionally requires exact EAPI 7/8 Portage image-tree parity
  for the core install/config/service/document/link helper corpus.
- [x] Environment snapshot comparisons with Portage. The opt-in live lane runs
  the same synthetic compile phase under Portage and Arise for EAPI 7 and 8,
  normalizes disposable absolute paths, and requires exact identity, phase,
  root and build-directory environment equality.
- [x] Failure injection at every phase. EAPI 7 and 8 have a
  failure-preservation matrix for every declared setup, source and lifecycle
  phase. Each worker exits nonzero after emitting output; the durable log
  retains pre-failure output, the exact terminal exit code and a terminal-error
  record, and the returned error names the preserved path. Production rebuild
  coverage additionally injects a failing `pkg_postinst` for both EAPIs after
  the journaled merge begins and proves payload/VDB rollback, a durably
  rolled-back journal and preserved terminal log. Broader process-death
  injection at journal persistence boundaries remains the P6 recovery gate.
- [ ] Logging differential against Portage covering successful and failed
  builds, parallel jobs, split/compressed logs, phase/QA messages, filtering,
  permissions, cleanup and interrupted-log recovery. Assert that a failed build
  prints the durable log path and preserves all output through the first error.

Acceptance gate:

- Synthetic EAPI 7/8 coverage and the initial real binary-package rehearsal
  produce equivalent image trees, lifecycle effects and durable logs under
  Portage and Arise. Signal remains compatibility coverage, not the primary
  resolver performance benchmark.

## P4R — representative package certification

- [x] Build and differentially certify trivial, autotools, CMake, Meson, Go,
  Python, Rust, kernel-module, binary-only and config-protected packages.
  A disposable synthetic corpus now certifies trivial shell, autotools-style
  EAPI defaults, CMake, Meson, Go, Python, Rust, kernel-module, binary-only and
  config-protected packages for both EAPI 7 and 8: Portage and Arise execute
  unpack through install/test and produce exact normalized image
  paths/types/modes/content. Installed executables pass the same smoke output,
  the kernel fixture produces the same relocatable ELF module image, and the
  protected-config fixture preserves a local edit while producing the same
  `._cfg0000_` update in disposable merge roots. Real apulse CMake and xrandr
  Meson disposable-root rehearsals remain additional live coverage.

Acceptance gate:

- Representative package families produce equivalent image trees and lifecycle
  effects under Portage and Arise without expanding the P4 core ABI gate.

## P5 — fetch and verification

- [~] Reuse DISTDIR atomically and avoid duplicate concurrent downloads. A
  shared Fetcher coordinates identical Manifest identities per DISTDIR, reuses
  valid files and commits unique temporary files by atomic rename. Linux
  processes additionally coordinate through symlink-safe, context-cancellable
  per-artifact flock files; subprocess fixtures prove contention and release.
  Resumable partial state remains. Production source fetch-only and legacy
  rebuild preparation call the same AcquireManifest entry point. A real root
  fetch of Signal's previously absent 110 MiB distfile completed and passed
  Manifest verification through Arise.
- [~] Implement Manifest digest and size verification. Strict DIST parsing,
  safe names, conflicting-record rejection, BLAKE2B/SHA256/SHA512 verification,
  enabled SRC_URI conditionals, rename syntax and ordered same-name fallbacks
  now produce a typed verified artifact set. Full Manifest policy parity and
  additional algorithms remain.
- [~] Implement GENTOO_MIRRORS and `mirror://` expansion. Effective
  GENTOO_MIRRORS supplies `mirror://gentoo`, selected repositories provide
  ordered profiles/thirdpartymirrors groups, and unknown or unsupported groups
  fail explicitly. Master-repository stacking and RESTRICT mirror policy remain.
- [~] Implement ordered URI fallback and rename syntax. Rename destinations,
  repeated same-destination sources and ordered mirror endpoints share one
  Manifest identity and advance only after transport or verification failure.
  Full Portage primaryuri/mirror restriction ordering remains.
- [ ] Implement supported fetch-command overrides and protocols.
- [ ] Implement partial download resume safely.
- [~] Implement RESTRICT=fetch and pkg_nofetch behavior. Restricted acquisition
  now verifies an existing Manifest-valid DISTDIR entry without contacting the
  network; a missing/corrupt entry returns a typed manual-fetch result, runs the
  package's `pkg_nofetch` through the versioned worker before build/mutation and
  preserves its elog diagnostics. An explicit ebuild `pkg_nofetch` also runs
  after ordinary source acquisition failure without requiring RESTRICT=fetch.
  Inherited/eclass-exported override discovery and parallel-fetch duplicate
  suppression remain before this item is complete.
- [ ] Integrate VCS fetchers through eclasses/execution ABI.
- [x] Deduplicate network work across concurrent package builds. One Fetcher
  coalesces goroutines, and per-artifact Linux flock files coordinate separate
  Fetchers and processes through verified atomic commit.

### Tests

- [ ] Local HTTP fixture for redirects, ranges, failures and fallback.
- [x] Digest mismatch and corrupted-cache tests, including preservation of an
  existing cache entry when every replacement source fails verification.
- [x] Concurrent request deduplication tests across goroutines, independent
  Fetchers and subprocess lock contention/release.
- [x] Offline synthetic rebuild using a complete Manifest-verified DISTDIR and
  an intentionally unreachable upstream URI.
- [x] Fetch progress events distinguish cache checking/reuse, active transfer,
  Manifest verification and atomic completion. The human CLI renders filename,
  bytes, percentage and transfer rate on a terminal, emits stable stage lines
  when redirected, and remains silent under `--quiet`; event and rendering
  regressions cover downloads and verified-cache reuse.

Acceptance gate:

- Fetch-only and normal builds never consume unverified distfiles when a
  Manifest digest is available.

## P6 — transactional merge and unmerge

- [~] Design and implement an operation journal. A versioned, fsynced,
  path-confined undo journal now records regular-file, directory, symlink and
  absent preimages before mutation; it rejects filesystem-root transactions,
  lexical escapes, symlink-ancestor escapes and unsupported object types.
  Journals durably distinguish active, committed and rolled-back operations,
  reopen after process loss, and deterministically recover active operations.
  The disposable phase-protocol merge path now journals every image target and
  its new VDB directory, rolls back an injected mid-tree failure, and commits a
  durable success record. Production merge/unmerge now uses the journal,
  including replacement VDB entries and lifecycle execution. Broader metadata,
  ownership/config-protection edge cases, operator UX, and other lifecycle
  mutation classes remain. Mutation entry
  points recover active journals while holding the VDB lock; exhaustive
  process-death tests now kill merge and unmerge after payload/VDB mutation but
  before commit, then prove the public retry rolls back the active journal and
  commits a clean replacement operation. A subprocess matrix additionally
  kills the journal after begin, every supported preimage capture and mutation,
  and commit, covering absent, regular-file, directory and symlink state.
- [x] Replace per-entry journal synchronization before further large live
  mutation testing. A 2026-07-21 `gentoo-sources-7.1.3` run captured roughly
  100,000 paths at about 80 paths/second because every entry performed an
  independent `fsync`, producing 20-minute-class transaction latency and severe
  filesystem/SSD write amplification for a package estimated at 56 seconds.
  Implement absent-subtree coalescing, bounded write-ahead group commit, a
  persistent segmented writer, merge progress, crash-boundary tests, and a
  kernel-source performance gate as specified in
  `docs/planning/PERFORMANCE_IMPROVEMENT_PLAN.md`. Version-4 absent-subtree
  records, two-pass preimage batching, grouped backup/payload durability and
  rate-limited path progress now bound durability barriers independently of
  path count. SIGKILL recovery passes immediately after preimage publication,
  after mutation before payload sync, and after payload sync before commit. A
  20,000-file fixture uses 11 fixed `fsync`s when rolled back and 11 plus one
  grouped `syncfs` when committed, lifting the large-world testing block.
- [ ] Make journal inspection and recovery operator-friendly. The staged
  design in `docs/planning/JOURNAL_RECOVERY_UX_PLAN.md` replaces the default
  flat history dump with active-first diagnosis, exact next-step guidance,
  per-operation inspection, versioned metadata and JSON, corruption handling,
  disk-usage reporting, and conservative retention that never prunes active or
  recovery-incomplete evidence.
- [~] Run collision/ownership validation before live-root mutation. The
  transactional merge path now runs the VDB ownership scanner before journal
  creation and rejects foreign-owned image targets without mutation. Production
  merge wiring is covered; the full FEATURES and Portage differential matrix
  remains gated.
- [~] Implement CONFIG_PROTECT and CONFIG_PROTECT_MASK. Transactional merges
  preserve changed regular files beneath protected paths using deterministic
  `._cfg0000_` names, advance past existing pending files, honor mask precedence,
  and consume the effective Portage configuration. Type transitions, symlink
  cases and Portage differential fixtures remain.
- [~] Preserve modes, ownership, timestamps, symlinks, hardlinks, xattrs, ACLs
  and file capabilities. Regular files now preserve exact permission/special
  bits, numeric uid/gid and mtimes; image hardlinks remain one installed inode.
  Newly created directories preserve mode, ownership and final mtime after child
  installation, while symlinks preserve numeric ownership and no-follow mtime.
  Linux extended attributes are copied natively for regular files, directories
  and symlinks and fail the transaction visibly on loss. Privileged
  `system.posix_acl_*`/`security.capability` fixtures, existing-directory policy
  and Portage differentials remain.
- [~] Generate complete Portage-readable VDB entries. Transactional merges now
  publish CONTENTS, the exact ebuild, environment snapshot, CATEGORY, PF, EAPI,
  SLOT, repository, USE, all five dependency classes, IUSE, REQUIRED_USE,
  LICENSE, PROPERTIES, RESTRICT, DEFINED_PHASES and INHERITED. CONTENTS paths are
  now Portage-compatible and root-relative. Native Go ELF inspection publishes
  both legacy NEEDED and NEEDED.ELF.2 without executing target binaries or
  invoking ldd/scanelf/readelf. BUILD_TIME and the global/package COUNTER are
  allocated while holding Portage's VDB lock and roll back with the transaction.
  A normalized, deterministically ordered package environment is written as a
  native Go-generated environment.bz2, fsynced before commit and verified by
  rebuild-level decompression tests. Remaining environment variables are tracked
  by the P4 Portage differential gate.
- [~] Implement replacement ordering and safe old-version unmerge. Transactional
  same-version reinstall now snapshots and replaces an existing VDB tree, while
  an upgrade publishes the new VDB, removes obsolete old payload paths only
  when absent from the new image and unowned elsewhere, then removes the old
  VDB in the same journal. An injected failure after payload cleanup restores
  the old payload/VDB and removes the new generation. Standalone removal now
  requires an explicit ROOT, preserves payload owned by another VDB entry, and
  journals payload plus VDB removal; injected finalization failure restores
  both. Transaction callback ordering for old-package `pkg_prerm`/`pkg_postrm`
  and new-package `pkg_postinst` is covered, including rollback after a failed
  post-removal hook. A production-path disposable-root matrix now covers fresh
  install, upgrade, downgrade, same-version reinstall, parallel-slot
  coexistence and ownership-aware removal with a committed journal at every
  step. Connecting those callbacks to the old and new versioned
  P4 workers, capturing arbitrary lifecycle writes in the journal, and exact
  Portage ordering differentials remain.
- [~] Implement collision-protect and protect-owned. The ownership scanner is
  now a mandatory preflight before journal creation or mutation; only the exact
  CP being replaced is excluded. A foreign-owner collision leaves files, VDB
  and journal directory untouched. FEATURES policy and full collision matrices
  remain.
- [x] Integrate preserve-libs and rebuild records. Replacement merges with
  `FEATURES=preserve-libs` compare old/new provided SONAMEs against installed
  VDB consumer edges, retain the required old objects and write Portage's JSON
  `preserved_libs_registry` format inside the payload journal. A later consumer
  rebuild that drops the final NEEDED edge removes the unowned preserved paths
  and registry record in its own transaction, including obsolete paths that
  Portage records in the current provider's `CONTENTS`; that ownership metadata
  is rewritten atomically with the cleanup. Failure before commit restores the
  consumer VDB, provider ownership, libraries and registry together; the
  existing structured preserved-rebuild scanner consumes the resulting record.
- [~] Commit VDB and world changes atomically with filesystem state. World-file
  updates now hold Portage's sibling lock across load/mutate/save and use a
  same-directory, mode-preserving, file-and-directory-fsynced atomic rename;
  the CLI deselect path uses this transaction. Coupling requested world additions
  to the payload/VDB journal in one recoverable operation remains.
- [x] Implement rollback/recovery after interruption or process death. Merge
  and unmerge now recover active journals immediately after taking the VDB lock,
  before inspecting potentially partial filesystem or VDB state. Real SIGKILL
  tests cover the terminal pre-commit mutation boundary and deterministic retry,
  plus every public durable journal transition and supported preimage kind.
- [~] Serialize or conflict-partition live merges. Merge and standalone unmerge
  now hold Portage's VDB mutation lock across ownership validation, journal
  recovery, filesystem/VDB mutation and commit. Finer-grained conflict
  partitioning and scheduler integration remain.

### Tests

- [ ] Merge/unmerge round-trip property tests.
- [ ] Compare image root and VDB with Portage fixtures.
- [ ] CONFIG_PROTECT behavioral matrix, including differential fixtures against
  Portage merge output and subsequent dispatch-conf/etc-update handling in a
  disposable ROOT.
- [~] File ownership and cross-package collision fixtures cover both the scanner
  and transaction-integrated foreign-owner rejection before journal creation;
  FEATURES combinations and broader ownership matrices remain.
- [x] Kill process at every journal boundary and recover. Subprocess SIGKILL
  coverage enumerates begin, capture and post-capture mutation for absent,
  regular-file, directory and symlink preimages, plus commit. Separate killed
  merge/unmerge processes prove recovery and retry after full payload/VDB
  mutation and before commit.
- [ ] Concurrent non-conflicting and conflicting merge tests.

Acceptance gate:

- An interrupted operation is either fully committed or recoverable, and
  Portage can read and manage the resulting VDB state.

## P7 — dependency-aware concurrent scheduler

- [x] Promote dependency-aware concurrency before using Arise for the full
  development-host world upgrade. Production execution now honors package
  `--jobs`, starts only dependency-ready actions, and serializes ROOT/VDB
  mutation with an internal commit lock while retaining the operation-wide
  Portage-compatible exclusion lock.
- [x] Split source execution at an explicit prepared-image/commit boundary.
  Fetch/unpack/prepare/configure/compile/test/install-to-image may occupy
  dependency-ready worker slots; merge, replacement lifecycle, journal/VDB
  commit and post-commit lifecycle use a separately serialized resource. Keep
  completed images available while waiting for their commit turn and account
  for their disk usage.
- [x] Replace FIFO worker distribution with a DAG ready queue. Resolver actions
  retain stable exact prerequisite identities in durable plans; SCCs are
  condensed and their members serialized in deterministic plan order.
- [x] Release dependents only after successful dependency completion. In the
  initial conservative scheduler, every prerequisite must commit before its
  dependent begins; separating build-time from commit-time edges can widen
  concurrency later without weakening correctness.
- [~] Propagate failures and implement keep-going by independent subgraph. The
  scheduler currently cancels and joins running peers on the first failure and
  never releases its dependents. Complete the commit-aware recovery contract in
  `docs/planning/EXECUTION_RECOVERY_PLAN.md`: drain independent work, roll back
  only uncommitted failures, take a locked actual-state snapshot, re-resolve the
  original goals, and automatically continue only when the recalculated DAG is
  a verified subset of the approved action identities.
- [ ] Add bounded retry/rebatch cycles behind an experimental option (working
  name `--keep-retrying`, with explicit cycle and per-package attempt limits).
  Retry only classified transient failures or actions whose relevant state
  fingerprint changed; stop on repeated no-progress fingerprints, permanent
  failures, approval expansion, cancellation, or thresholds. A changed solution
  must be saved and separately approved rather than inheriting the original
  plan SHA.
- [x] Apply load-average backpressure in production workers. Linux loadavg
  sampling is context-cancellable and gates each DAG worker before package
  startup.
- [ ] Model CPU, memory, network and merge resources separately.
  Package worker count (`--jobs`) and per-package build parallelism (`MAKEOPTS`)
  are independent, matching Portage; Arise no longer rewrites `MAKEOPTS` to the
  package-worker count.
- [x] Support fetch-ahead while respecting build and merge ordering.
- [~] Match Portage source-candidate policy. Ordinary SRC_URI artifacts now try
  configured Gentoo mirrors before upstream, fall back after Manifest size or
  digest failure, honor USE-resolved `RESTRICT=mirror` and `primaryuri`, and
  retain explicit `mirror://` expansion. Remaining parity includes mirror
  layout discovery/cache, `fetch+`/`mirror+`, local and read-only DISTDIRs,
  bounded checksum retry policy, and mismatch quarantine naming.
- [x] Promote bounded parallel fetch-ahead and dependency-ready preparation.
  Source
  acquisition uses a separate `--fetch-jobs` pool (default four) for both
  fetch-only and live execution, retains Manifest verification, coalesces shared
  distfiles through the fetcher's process/file locks, and completes before any
  package mutation. Dependency-ready source preparation now runs concurrently,
  while commits remain serialized behind the VDB/ROOT mutation boundary.
  Concurrent terminal fetches use
  complete event-per-line output because a single carriage-return percentage
  line cannot safely have multiple owners; single-worker fetching retains the
  animated percentage/rate display. The first live 369-action fetch-ahead run
  completed successfully with four workers against the normal DISTDIR. No
  abandoned `.part-*` files remain; `/var/cache/distfiles` is 13 GiB and the
  root filesystem retains 27 GiB free afterward. The run exposed and directly
  motivated the concurrent newline fix above.
- [ ] Make output deterministic with per-package event streams.
  Production parallel mode emits one complete `Emerging (N of M)` line per
  started action and one completion line per committed action instead of
  letting multiple workers overwrite a single spinner label. Full compiler
  output remains isolated in durable per-package logs. Large image-install
  progress now rewrites one transient TTY line; redirected output records only
  ten-percent milestones and completion rather than one line per callback.
  Parallel package merges no longer share that transient cursor or percentage
  bucket: each package emits one durable merge-completion line before its next
  stage message, preventing concurrent stage and progress text from colliding.
- [ ] Make the package transaction the primary fetch display hierarchy, as
  emerge does: identify the owning package for each fetch, keep per-file
  percentages transient on a TTY, route concurrent detail to a durable fetch
  log, and retain concise package/job/load/merge-wait status on the terminal.
- [ ] Emit structured rebuild-cause provenance in human and JSON plans,
  including the triggering package and edge for subslot, changed-USE,
  changed-dependency, preserved-library, and explicit reinstall actions.
- [ ] Throttle new package jobs as PORTAGE_TMPDIR free space falls. Model
  emerge's `--jobs-tmpdir-require-free-gb` behavior, account for completed
  images waiting on the commit lock, and report why concurrency was reduced.
- [x] Persist completed nodes for resume. Completion is fsynced from the
  transaction callback while the internal commit lock remains held, before a
  second worker may mutate ROOT/VDB.
- [ ] Investigate pathological durability latency on the serialized commit
  path. During the 2026-07-21 OpenSSL continuation attempt, the terminal
  remained at `Installing package` for roughly 20 minutes after the external
  worker exited. The Arise process had no children, held the operation-wide VDB
  lock, retained a second package log, and one Go thread waited in
  `jbd2_log_wait_commit` while the remaining threads were parked on futexes. The
  eventual result was a merge ownership rejection, not a storage error. Add
  stage spans around journal capture/sync/commit, VDB metadata sync, resume-file
  file and directory sync, log finalization, and commit-lock wait/hold time.
  Capture the exact blocked syscall and descriptor with a disposable-root
  `strace`/fault-injection fixture. Determine whether latency comes from the
  underlying filesystem, excessive or badly ordered durability barriers, or
  unrelated work performed while holding the commit lock. Preserve crash and
  recovery guarantees; never optimize this by simply deleting required
  `fsync`/directory-sync operations.
- [x] Avoid rebuilding successful packages after a downstream failure.

### Tests

- [x] Assert no dependent begins before every required predecessor succeeds.
- [x] Assert independent branches run concurrently. Both mock scheduler tests
  and a real two-package phase-protocol integration prove overlapping builds,
  single-writer commits, valid journals/payload/VDB entries and empty resume.
- [ ] Deterministic event/output test across repeated runs.
- [~] Load-average and cancellation tests. Failure cancellation and peer join
  are covered; synthetic load transitions remain.
- [~] Race detector and stress tests with hundreds of synthetic nodes. Targeted
  executor/resolver/rebuild/CLI race suites pass; the large synthetic stress
  matrix remains.

Acceptance gate:

- Scheduler is dependency-correct, deterministic, resumable and measurably
  faster than serial execution on parallel graphs. On a same-plan integration
  corpus, `--jobs` must change observed build overlap, preserve deterministic
  concise terminal events and per-package logs, and avoid a material throughput
  regression against emerge at the same job/load settings.

## P8 — wire install, update and removal end to end

- [~] Establish the Arise-owned filesystem contract: administrator settings in
  `/etc/arise`, durable state in `/var/lib/arise`, cache in `/var/cache/arise`,
  logs in `/var/log/arise`, runtime coordination in `/run/arise`, and build or
  transaction scratch in `/var/tmp/arise`. Continue reading `/etc/portage` as
  shared Gentoo policy, but never write Arise-only syntax there; anything
  emitted into a Portage namespace must remain valid and useful to Portage.
  Work/resume/journal paths, native package logs, the metadata database, and
  Portage-compatible VDB/world/emerge.log projections are implemented. General
  `/etc/arise`, cache/runtime placement, migration, retention, and permissions
  policy remain.

- [~] Connect resolved plans to fetch/build/binpkg/merge/unmerge execution.
  Verified source plans now execute through fetch, phase workers, image
  preparation, serialized journaled merge/unmerge, resume, and final
  verification. Local/remote binary-package production and consumption remain
  P9 work, and depclean/prune still stop before mutation.
- [~] Correctly implement pretend, ask, fetchonly and buildpkgonly. Source
  fetch-only now executes the resolved plan through the shared Manifest-backed
  verified DISTDIR pipeline without entering build or merge. Human output calls
  this a fetch plan, labels entries as fetches and reports packages to fetch
  rather than misleadingly promising installs; binary fetch,
  complete ask semantics and buildpkgonly remain.
- [x] Update world only after successful explicit installs and respect oneshot.
  Selection is derived before execution, published after successful completion,
  and covered by explicit-target, set-target, dependency-only, and oneshot
  tests.
- [x] Mark resume nodes complete only after transaction commit. Versioned resume
  persistence, committed-prefix skipping, recovery-before-resume, failure-stage
  retries, and skip-first behavior have executor fixtures.
- [x] Implement executable `update @world` planning and transactions.
- [ ] Implement custom package sets and list-sets behavior.
- [x] Implement exact uninstall with whole-state reverse-dependency and reverse-
  ELF safety, locked state revalidation, lifecycle hooks, and journaled
  unmerge. General atom expansion and depclean-style selection remain separate.
- [ ] Implement depclean and prune execution with confirmation and journaling.
- [x] Implement locked atomic deselect and successful-install world selection.
- [ ] Wire preserved-rebuild scheduling through the safe planner/executor.
- [~] Handle signals and cancellation without corrupting state. The command
  context handles SIGINT/SIGTERM; resolver, fetch, phase-worker process groups,
  scheduler, merge, and journal paths have bounded cancellation tests.
  Differential interruption tests at every public mutation boundary remain.

### Mutation-readiness critical path

The initial serial live-mutation gate has been promoted through bounded
dependency-aware execution. No package may enter its first mutable phase until
the complete selected plan has passed the applicable whole-plan checks below.

- [~] Build the production action executor: resolved action -> repository/VDB
  identity -> Manifest-verified artifacts -> typed P4 request -> durable
  per-package log -> image tree -> P6 merge/replacement/unmerge -> terminal
  result. Disposable and live-root source install, reinstall, upgrade,
  lifecycle, exact removal, bounded dependency-aware multi-action scheduling,
  committed-prefix resume, and serialized commits are wired. Binary-package
  breadth, general depclean/prune/removal planning, preserve-libs closure, and
  wider EAPI/helper/package certification remain promotion gates.
- [~] Preflight the selected ebuild, EAPI, inherited eclasses, exported/default
  phases, helper closure, FEATURES/RESTRICT/PROPERTIES, isolation backend,
  writable paths, log destination, disk space and every artifact before worker
  startup. Unsupported behavior must reject before mutation. The executor now
  preflights every selected action before its first worker, and
  `--preflight-only` audits a verified large plan without applying the live
  action-count/removal canary or fetching, building, journaling, locking or
  mutating package state. Shell-aware comment/brace parsing fixed false
  failures in foundational ebuilds using
  `${var##pattern}`, quoted braces and inline metadata comments, and
  `RESTRICT=binchecks` is recognized as disabling a check Arise does not run.
  Live phase discovery uses the same Portage isolation policy as ordinary
  execution; bubblewrap is not a live-root prerequisite. Active custom lifecycle hooks now
  fail closed during live-root preflight because normal execution intentionally
  uses Portage's unsandboxed lifecycle policy and those writes are not yet part
  of the payload journal. Custom `pkg_postinst` is admitted separately because
  it runs only after payload/VDB journal commit; failures are durable
  committed-state maintenance errors and resume must not rebuild the package.
  Live `pkg_pretend` likewise follows Portage's free-phase policy.
  Custom `pkg_setup` and `pkg_preinst` no longer block the default lane:
  Portage executes them without transactional arbitrary-write capture, so
  Arise mirrors that behavior. Stronger capture is additional and must be
  USE-gated. The dependency-free implementation contract is frozen in
  `docs/planning/LIFECYCLE_TRANSACTION_PLAN.md`: a native Go/Linux syscall
  observer stops each lifecycle mutation, captures its durable preimage, and
  only then resumes it, while leaving Portage sandbox/privilege/namespace
  semantics unchanged. Journal ownership must move before the first actual
  lifecycle ROOT write and remain shared through payload/VDB commit. The first
  Linux/amd64 prototype now decodes principal pathname mutations, follows a
  forked grandchild, durably captures an overwrite with the real journal, and
  proves that injected capture failure prevents the stopped syscall and reaps
  the trace tree. Coverage now includes `openat2`, fd-only truncate/mode/owner/
  xattr changes and shared writable mappings; mount/root changes and
  mount-capable namespace transitions fail before execution. It remains
  disconnected from live execution until the remaining syscall/ABI,
  cancellation and worker-protocol promotion matrix passes; it is not a
  prerequisite for the Portage-compatible world update.
  On 2026-07-22 the normal static binary passed all 273 install actions in
  `--preflight-only -uDN @world` with no package-state mutation. The final two
  real pretend failures were cleared by supplying implicit `ARCH=amd64` in
  phase `USE` (required by google-chrome's `pkg_pretend`) and locating
  `PORTAGE_TMPDIR` on the spacious `/home` filesystem for Firefox's 9 GiB
  requirement. Arise now adds every package-owned work/build/source/image/temp/
  home/log path to `SANDBOX_WRITE`, matching Portage when the build root is not
  under globally allowed `/tmp`.
  Portage-compatible post-commit lifecycle handling and installed VDB
  environment execution raise the same audit to all 369 install actions
  passing. The separately planned obsolete Perl removal exposed an installed
  `pkg_postrm` requirement; standalone uninstall lifecycle execution has since
  landed with installed-environment replay. The current host
  has `CONFIG_OVERLAY_FS` disabled, so lifecycle delta capture must not require
  overlayfs. Disk-space and full helper-closure certification remain.
- [~] Bind lifecycle execution to the transaction. `pkg_preinst`, old
  `pkg_prerm`/`pkg_postrm`, new `pkg_postinst`, and standalone removal hooks
  now have explicit phase ordering and failure boundaries. Controlled
  payload/VDB changes are journaled; the baseline follows Portage's
  committed-state semantics for write-capable postinstall/postremove hooks, so
  failures remain visible with durable logs and resume records the package as
  committed rather than rebuilding it. Old removal hooks execute from the
  installed VDB `environment.bz2`, with typed current ROOT/path values restored
  after sourcing. Arbitrary lifecycle ROOT writes remain outside the payload
  journal. Stronger rollback is an additional experimental plan-bound
  capability with overlay/fuse-overlay lifecycle delta and Btrfs/OpenZFS/LVM
  snapshot providers; see `docs/transaction-backends.md` and
  `docs/planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md`. Provider probes,
  complete mount/dataset/subvolume/LV coverage, capacity and boot recovery must
  be revalidated under the operation lock, and fallback must never silently
  weaken an explicitly approved experimental guarantee. This host is ext4 on `/dev/dm-1`, has no
  overlayfs kernel support, fuse-overlayfs or Btrfs, and requires privileged
  LVM inspection before snapshot eligibility is known.
- [~] Implement each rollback provider behind an independent Gentoo USE gate.
  A command-free prototype now defines the fail-closed provider interface,
  longest-boundary mount coverage evaluation, explicit nested-mount
  exclusions, capacity/activation validation, and a versioned, digested,
  atomically published recovery record. It intentionally cannot execute a
  snapshot or rollback. Native Btrfs, OpenZFS, LVM, kernel OverlayFS and
  fuse-overlayfs implementations, packaging gates, privileged integration and
  boot recovery tests remain.
  Btrfs, OpenZFS, LVM, kernel OverlayFS and fuse-overlayfs must have separate
  dependency mappings plus enabled/disabled build and runtime tests. Enabling
  one provider must not pull in or authorize another; the static journal and
  recovery baseline remains complete with every provider disabled.
- [~] Implement and certify EAPI 9 rather than aliasing it to EAPI 8. The phase
  protocol enforces Bash 5.3, keeps package-manager shell variables unexported,
  provides `pipestatus`/`ver_replacing`, bans `domo`/`assert`, and preserves
  absolute staged symlink targets while retaining the EAPI 7/8 rewrite. The
  first live world consumers, `dev-lang/go-1.26.4` and
  `dev-util/github-cli-2.93.0`, now pass whole-plan preflight. Broader EAPI 9
  eclass/helper corpus certification remains.
- [x] Couple successful explicit installs to a locked atomic world addition
  while preserving `--oneshot`; dependency actions and failed targets are not
  selected.
- [~] Add an administrator-facing recovery command that lists active journals,
  performs deterministic recovery under the VDB lock, reports preserved build
  logs, and is usable from the copied static binary. Status, targeted rollback
  and all-active rollback under the VDB lock are implemented; correlating and
  reporting preserved logs remains. Treat static linkage as a tested recovery
  invariant, not a naming convention: release/preserved binaries use
  `CGO_ENABLED=0`, must have no ELF interpreter or host libc dependency, and the
  static Make target now fails rather than merely printing a warning if linkage
  regresses. An ad-hoc development build caught by `file`/`ldd` demonstrated why
  this must be verified at the artifact boundary.
- [ ] Make optional integration dependencies packaging-explicit. The default
  installed Arise artifact must remain a standalone `CGO_ENABLED=0` static
  executable with a fully functional baseline when optional integrations are
  absent. Any feature that would introduce an additional Arise runtime or
  linkage dependency must provide a safe fallback, be disabled independently,
  and be controlled by an explicit ebuild USE flag with enabled/disabled build
  and runtime tests. Bubblewrap, snapshot providers, FUSE helpers, external QA
  tools and richer log sinks are enhancements, never prerequisites for core
  inspection, resolution, preflight, recovery or Portage-compatible execution.
  Add and test the concrete IUSE/package dependency mapping with the repository
  ebuild; no ebuild is currently maintained in this source tree.
- [~] Maintain whole-action eligibility checks for promoted live lanes.
  It must reject blockers, preserve-libs transitions, unsupported helpers or
  policy, unverified artifacts, foreign-owner collisions, unsafe lifecycle
  writes and any action outside the approved exact CPV/slot/repository plan.
  Current source lanes enforce exact source/ROOT actions, whole-plan preflight,
  lifecycle policy, collision/ownership checks, locked state revalidation and
  state-bound approval. Bounded multi-action plans and serialized commits are
  promoted; preserve-libs and broader blocker/policy evidence remain for later
  lanes. The first live
  `media-sound/apulse-0.1.14` install passed with a committed 47-entry journal,
  root-owned payload/VDB, expected CONTENTS and ELF linkage. Same-version
  reinstall then passed with a second committed 47-entry journal and intact
  VDB/CONTENTS/linkage. The one-action xrandr 1.5.3 -> 1.5.4 disposable upgrade
  and subsequent 78-entry live upgrade also pass; the old VDB is absent and the
  1.5.4 binary/CONTENTS/linkage verify. The state-bound live deselect of obsolete
  `net-dns/bind-tools` passed with only that world line removed. Its separate
  empty-payload VDB removal then passed with a committed 28-entry journal.
  Selecting the already-installed `net-dns/bind` provider into world without a
  rebuild subsequently passed with an exact world delta; a live `dig` query
  confirmed the replacement client works. Exact parsed VDB matching prevents
  `bind-tools` or another hyphenated sibling from satisfying selection of
  `bind`.
- [!] Freeze pre/post evidence for every live gate: plan JSON, repository/profile/
  VDB/world fingerprints, CONTENTS ownership, preserved-libraries registry,
  dynamic linkage, journal state, durable log path and final verifier result.
  Verified JSON plans can now be atomically saved by path or name beneath a
  configurable default plan directory and later supplied with `--approve-plan`;
  the embedded SHA-256 remains the enforced authorization primitive while the
  raw digest flag remains available for automation.

The larger set gates additionally require:

- [x] Preflight every action in the plan before the first merge; a late
  unsupported EAPI/helper/policy failure must not be discovered after package
  state has already changed.
- [x] Persist resume state only after each package transaction commits, recover
  interrupted active journals before resume, and prove deterministic restart
  after fetch, build, lifecycle and merge failures. Commit-then-resume ordering
  is wired and a four-stage executor matrix proves that the committed prefix is
  skipped while the failing package and dependent tail retry deterministically.
  Resume initialization, completion and `--skipfirst` updates now use an
  fsynced same-directory atomic rename instead of truncating the sole record.
- [x] Execute serially until P7 is certified. Serial execution is acceptable
  for the initial safety gates; concurrency must not be introduced merely to
  make a large live rehearsal finish sooner. The original one-or-two-action
  live canary limit is promoted to arbitrary verified install-plan length only
  when durable resume state is configured; every action kind is checked before
  preflight, the entire plan preflights before mutation, and execution remains
  serial under the operation VDB lock with state-bound revalidation.
  The live executor writes each package's complete phase output to its own
  durable `/var/log/portage`-compatible log and presents package ordinal/name,
  completion events and optional estimates on the terminal. Future P7 parallel
  execution must retain per-package logs and multiplex only concise status;
  interleaved compiler output is not an acceptable default. Rich split/tabbed
  viewers may consume the logs but are a separate UI, not an executor protocol.
- [!] For `@system` and `@world`, require a conflict-free portable snapshot,
  equivalent normalized Portage plan where Portage resolves, and verified
  post-transaction state. A stronger Arise repair requires a fixture-backed
  cause and final-state proof.
  After the live Qt5 retirement, a deep/newuse `@system` run that accidentally
  omitted `--complete-graph` completed in 2.13 seconds with zero backtracks and
  correctly rejected its 158-action plan on 13 retained reverse-dependency
  constraints (twelve Python target edges and one Perl any-of). Repeating the
  gate with the required `--complete-graph` option produced the same normalized
  159-action, zero-conflict, verified plan three times; the 13 diagnostics were
  therefore an invocation error, not a post-checkpoint resolver repair list.
  Treat package-manager self-upgrades as an evidence-driven chunk rather than
  an unconditional special case. On the current host, shallow oneshot Portage
  leaves seven retained Python-target constraints invalid, while its verified
  complete-graph closure expands to 207 actions. It is therefore not a useful
  independent bootstrap chunk for this transition and must remain inside the
  verified world transaction unless a smaller verified frontier is found.
- [!] Before `--emptytree`, prove boot-critical replacement ordering, static
  recovery-binary availability outside paths being replaced, bounded disk
  usage, resume/recovery after interruption, and no removal of a working
  package generation before its replacement commits.
  Inventory host-tool dependencies used by the phase worker (`bash`, `tar`,
  `xz`, `bzip2`, `zstd`, `unzip`, `ar`, `patch`, compilers and package-specific
  tools). Add native archive readers only as a bounded recovery/bootstrap
  fallback with traversal, ownership, link and decompression-bomb defenses;
  native extraction alone must not claim arbitrary source-build independence
  from a damaged host. Prefer preserved static recovery tools and verified
  binary-package/bootstrap artifacts for restoring the wider toolchain.
  Define Arise package USE flags for optional compiled recovery capabilities
  (for example native `zstd`/`xz` and archive extraction) only when the resulting
  binary capability set is introspectable and recorded in executable plans.
  Keep host-tool providers as a small default where appropriate. Do not describe
  a limited Go command runner as a shell replacement: ebuild/eclass execution
  requires real Bash compatibility, and a preserved static Bash is preferable
  to accidentally starting a second shell-language implementation.
  Bound the recovery nucleus to static Arise plus a known-good static Bash and
  the minimum code needed to inspect/authorize/recover journals and restore
  verified artifacts. Do not accumulate static compiler/linker/toolchain copies.
  If GCC, libc headers, the linker or broader build environment is damaged,
  pivot to verified binary packages, a tinderbox/stage artifact or external
  rescue media.
  Investigate an optional pure-Go recovery linker behind an Arise package USE
  flag, but define its contract narrowly: inspect or relink known verified
  recovery artifacts for explicitly supported ELF architectures, object forms
  and relocation sets. It is not a claim to replace GNU `ld`/LLD for arbitrary
  C/C++ ebuilds. Plans and `arise info` must expose whether this capability was
  compiled in, and unsupported inputs must fail closed to the external recovery
  path rather than producing a best-effort binary.

### Tests

- [x] End-to-end install into an isolated ROOT. The phase-protocol source path
  installs payload and Portage-readable VDB state with a committed journal.
- [x] Upgrade, downgrade, reinstall, slot coexistence and replacement tests.
  The disposable-root production path removes obsolete generations, preserves
  both parallel-slot VDB entries and commits every transition durably.
- [~] End-to-end uninstall/depclean/prune tests. Transactional unmerge now
  removes an exclusively owned parallel-slot payload while retaining paths
  co-owned by the surviving slot; planner-driven depclean/prune remain.
- [x] Resume after injected fetch, build, lifecycle and merge failures. Public
  merge and unmerge retries recover real process death at the pre-commit
  boundary; the executor matrix retains exactly the failing action and tail for
  all four stages, then completes them without rerunning the committed prefix.
- [x] Verify pretend performs zero mutations. A copied-binary live check matched
  the Signal plan while before/after VDB and world fingerprints were identical.

Acceptance gate:

- Arise can safely manage a disposable Gentoo ROOT through install, update,
  removal, interruption and recovery cycles.

### Live-operation promotion gates

These gates remain disabled until P6 journaling, rollback/recovery, VDB/world
atomicity and the corresponding disposable-root rehearsal pass. Each live gate
requires an approved exact plan, pre/post VDB, world, filesystem and preserved-
library snapshots, durable logs, whole-state verification and a tested recovery
path. Passing a later gate never retroactively waives an earlier one.

- [x] Upgrade one already-installed package successfully. The exact xrandr
  1.5.3 -> 1.5.4 transaction committed and the installed binary reports 1.5.4.
- [x] Install one previously uninstalled package successfully. The exact
  apulse-0.1.14 transaction committed and its wrapper, libraries and VDB were
  verified; an exact reinstall subsequently passed as well.
- [x] Deselect one world entry without removing its installed files.
  `net-dns/bind-tools` produced only the approved world-file delta.
- [x] Remove one installed package with reverse-dependency safety. The exact
  whole-state-verified bind-tools removal committed, after which the installed
  bind provider remained functional and was selected into world without a
  rebuild.
- [ ] Complete a preserved-rebuild update successfully, then verify linkage and
  remove obsolete preserved libraries only after no consumer remains. Native
  structured evidence separated 49 genuine preserved edges from 335 unrelated
  private/missing SONAME edges. The unavailable orphan M2Crypto consumer passed
  verified removal and committed a 208-entry live journal, reducing the set to
  9 packages/47 edges. The crda retry proved split-usr symlink journaling and
  committed a 58-entry removal, reducing it to 8/46. QtCore removal is now
  blocked by native VDB reverse-ELF verification naming its 13 installed
  consumers; the Qt5/PyQt5 retirement closure remains to plan. A state-bound
  `app-laptop/thinkfan-1.3.1` reinstall then committed successfully: its binary
  now needs `libyaml-cpp.so.0.8`, and a fresh scan fell from 7 packages/30 edges
  to 6/28 with exactly thinkfan's two 0.7 edges removed and no new edges. The
  old yaml-cpp 0.7 files remain owned by the current provider VDB on this live
  machine, so the promotion gate remains open pending state-bound cleanup and
  final filesystem verification. Core cleanup now handles that Portage
  ownership representation transactionally. A subsequent state-bound
  `net-libs/libsoup:2.4` upgrade from 2.74.2 to 2.74.3-r1 committed after an
  exact disposable upgrade rehearsal. The installed library now needs
  `libxml2.so.16`; the old VDB entry is gone, and the preserved scan fell from
  6 packages/28 edges to 5/22 with exactly libsoup's six `libxml2.so.2` edges
  removed and no new edges. The first bounded two-action live cluster then
  reinstalled `llvm-core/llvm-19.1.7` and upgraded Clang to
  `llvm-core/clang-19.1.7-r1` after both packages passed full disposable source
  builds. Both installed at SLOT `19/19.1`, LLVM now needs `libxml2.so.16`, and
  the preserved scan fell from 5 packages/22 edges to 3/14 with exactly the two
  LLVM/Clang packages and all eight expected `libxml2.so.2` edges removed and
  no additions. The remaining closure is Python 3.10 (three OpenSSL 1.1 edges),
  Python 3.9 (five OpenSSL/libffi edges), and repository-orphaned LLVM 13 (six
  libffi edges).
- [ ] Update `@system` successfully with a verified final state.
- [ ] Update `@world` successfully with a verified final state.
- [ ] Complete an `--emptytree` world update, proving the static Arise binary,
  operation journal and recovery tooling remain usable throughout the rebuild.
- [ ] Add opt-in historical build-time estimates per package and for the whole
  proposed action set, then show elapsed and estimated remaining time during
  execution. Keep the authoritative history as an append-only, versioned JSONL
  event log rather than an opaque database: one self-contained record per
  attempt with package/version/slot, machine and build-profile fingerprint,
  start/end/duration/outcome, jobs/load, relevant flags, plan identity and
  durable log/journal references. Append under a lock, sync completed records,
  tolerate and report a truncated final record, and rotate/archive without
  destroying source evidence. The history must remain directly useful through
  jq, Python, Perl, Ruby, gron and ordinary text tooling.
  Feed default estimates only from successful compatible samples, report sample
  count and action-set coverage, and retain failed/interrupted attempts for
  diagnostics. Median/percentile summaries and search indexes are disposable,
  atomically published accelerators that must be fully reconstructable from
  JSONL. Do not make Badger or another private database authoritative for build
  history. Estimate snapshots belong in the complete durable-plan metadata for
  local/tinderbox comparison but remain outside execution authorization unless
  a timing-related value such as jobs or load limit changes actual behavior.
  The staged estimator, live-progress, parallel-makespan and validation design
  is specified in `docs/planning/BUILD_TIME_ESTIMATION_PLAN.md`.
- [ ] Bring `arise sync` output to `eix-sync`-level operational usefulness:
  enumerate configured repositories and sync methods, show per-repository
  status and last/current timestamps, make cache/index refresh explicit, and
  summarize package changes concisely by default. Group changes by repository
  and action, collapse upgrades into `old -> new` transitions instead of
  reporting both versions as unrelated entries, and show per-repository,
  checkout, indexing and total elapsed times. Keep unchanged counts and the
  compact aggregate summary in normal output; reserve changed paths and other
  low-level churn for `--verbose`.
  The correctness floor is now mandatory in the command path: a successful
  repository checkout update is followed by atomic resolver-snapshot
  publication before `sync` reports final success.

### System-construction acceptance ladder

The detailed gate definitions and validation checklist live in
`docs/evidence/PORTAGE_SELF_HOSTING_MILESTONE_2026-07-24.md`.

- [x] G0: build, merge, commit, and finalize `sys-apps/portage-3.0.81.2`
  through Arise on the live Gentoo system.
- [ ] G1: start from a documented fresh stage3, complete an Arise-only deep
  world update, verify final state, and reboot. This proves Arise can maintain
  Gentoo.
- [ ] G2: complete and validate an Arise-driven empty-tree rebuild of the G1
  system, then reboot and repeat health checks.
- [ ] G3: construct a stage3-equivalent root from a defined stage1/bootstrap
  environment, with an explicit host-tool boundary and closure comparison.
  This proves Arise can construct Gentoo.
- [ ] G4: repeat the bootstrap from a clean snapshot, preserve complete
  evidence, compare normalized outputs, empty-tree rebuild, and reboot. This
  proves Arise is an independent, repeatable Gentoo bootstrap implementation.

G3 is a designed bootstrap experiment, not an assumption that an operator has
previously performed Gentoo's historical stage1 procedure. LFS experience is
directly relevant to toolchain ordering and chroot isolation, while Gentoo's
profile, USE, VDB, bootstrap-set, and package-manager semantics must be
specified and verified explicitly.

## P9 — modern binary package support

- [x] Implement current Gentoo GPKG reading and writing. Arise verifies the
  GLEP 78 outer container and GLEP 74 Manifest before reading metadata or
  streaming the image into the hardened extractor. It reads uncompressed,
  gzip, bzip2, xz, zstd, lz4, lzip and lzop inner members and writes
  deterministic zstd packages. Installed Portage and Arise cross-read each
  other's generated packages in the permanent integration lane.
- [x] Parse and generate Packages indexes. Parsing is bounded and rejects
  malformed records, duplicate keys/instances, unsafe paths and count
  mismatches; generation is canonical, atomic and directory-synced.
- [x] Support build IDs and multiple package instances. GPKG quickpkg output
  uses the Portage category/package/PF-BUILD_ID layout and Packages selection
  supports exact or newest build instances.
- [x] Verify digests and signatures according to policy. Recovery sets use
  a store-local Ed25519 trust anchor and fail closed on missing, mismatched or
  invalid signatures. GPKG verifies BLAKE2B/SHA512 Manifest coverage for every
  member before extraction, supports mandatory clear-signed Manifests through
  a caller-supplied verifier, and provides a trusted-keyring `gpgv` verifier.
- [x] Implement local and remote candidate selection. Local XPAK/GPKG
  candidates are selected by version/build identity, while remote selection
  consumes bounded `Packages` indexes, fetches the complete resolved dependency
  closure, verifies advertised size and BLAKE2B/SHA512 digests, validates the
  package container and publishes it atomically.
- [x] Implement binpkg USE/config compatibility. Selection checks the
  resolver-selected IUSE domain and enabled USE state together with available
  CHOST, ABI, repository, slot and subslot metadata.
- [x] Wire `-k`, `-K`, `-g`, `-G`, `-b`, and `-B` end to end. Binary actions
  extract into an isolated image and use the normal journaled merge path;
  acquisition implies binary selection, and build-package modes publish
  deterministic GPKG output before optional installation.
- [~] Absorb `quickpkg`'s role: create a metadata-complete binary package from
  an installed VDB instance, with explicit handling for preserved libraries,
  config files, hardlinks, sparse files, ACLs, xattrs, capabilities and build
  IDs. Treat missing, locally modified, type-changed and foreign-owned paths as
  explicit evidence rather than silently normalizing the installed image.
  `arise quickpkg --gpkg` now writes a Portage-readable GPKG containing only
  paths named by `CONTENTS` plus the complete flat VDB metadata set, while the
  default recovery form retains its stronger machine-bound provenance.
- [~] Harden host-derived binpkgs before using them as recovery artifacts.
  Record exact CPV/slot/subslot/repository/EAPI/USE/ABI/build identity, the
  complete source VDB entry and environment, per-entry types/hashes/ownership/
  modes/timestamps/linkage/extended metadata, ROOT/configuration/repository
  fingerprints, and recovery-set/operation provenance. Distinguish
  host-recovery artifacts from repository-built reusable packages in metadata,
  policy and user-facing output. Archive capture and extraction now preserve
  hardlinks, sparse extents, numeric ownership when privileged, special mode
  bits, nanosecond timestamps, xattrs, POSIX ACL xattrs and file capabilities.
  Extraction enforces entry, per-file, total expanded-byte, xattr and embedded
  metadata limits.
  The initial capture boundary now fails closed for malformed `CONTENTS`,
  missing or type-changed paths, changed symlink targets, ROOT escapes and
  symlinked source parents. Package identity cannot redirect publication, and
  completed XPAK artifacts are file- and directory-synced before success.
  Each artifact now embeds a versioned `host-recovery` manifest containing its
  package identity, complete source VDB file contents (including the saved
  environment), and canonical payload type/mode/UID/GID/size/mtime/link/hash
  evidence with separate VDB and ROOT digests. Reading the manifest verifies
  its digest, strict schema, evidence digests and agreement with ordinary
  package metadata. A versioned capture context now binds operation kind/ID,
  recovery-set ID, approved plan digest, Portage-configuration fingerprint and
  repository fingerprint. Standalone `quickpkg` captures hash the complete
  configuration tree but explicitly label their repository identity-marker
  fingerprint as partial; transaction callers can supply a complete selected
  source-closure fingerprint. Mutation-policy evidence and GPKG remain required
  before these artifacts qualify for automatic recovery.
- [~] Publish pre-update recovery sets atomically before live-root mutation.
  Every installed package that the approved plan may replace or remove must
  have a verified artifact, or the transaction must not begin. Retain the
  complete set through runtime/reboot verification; deduplicate by content
  digest while preventing collection of active, failed or pending-rollback
  sets. Restoration re-resolves the complete set against actual state, uses
  normal journaled transactions and requires separate approval for drift.
  The live install/update executor now publishes resolver-identified replaced
  instances through its locked pre-mutation gate. Artifacts are built and
  verified in a private staging directory, bound to one operation/set/plan,
  followed by a synced complete manifest and one directory rename. Capture,
  verification, cancellation or publication failure prevents resume creation,
  build workers and live-root mutation. Exact uninstall plans now publish the
  same complete set while holding the VDB lock and before opening the first
  installed lifecycle or unmerge journal. Immutable recovery objects separate
  content provenance from per-operation set provenance, are keyed by artifact
  SHA-256, and are hardlinked into every referencing set. Identical captures
  therefore share storage across operations; verified-set pruning collects an
  object only after no readable remaining set references it, and skips object
  collection entirely when a preserved set cannot be verified. Published sets
  now start
  `active`, transition to `pending-verification` only after successful package
  execution, and retain explicit `failed` and `pending-rollback` states.
  Conservative pruning removes only explicitly `verified` sets beyond the
  requested retention count; missing or malformed status and every other state
  are preserved. `recover verify-set` requires a kernel boot identity different
  from capture, and `recover prune-sets` exposes conservative retention.
  Recovery-set
  inspection verifies every artifact and constructs a reverse-capture-order
  restore plan. Configuration or repository drift produces a canonical approval
  digest; restore refuses mismatched, missing or unnecessary approval. Approved
  restores hold the VDB lock and use the normal journaled merge path for every
  artifact, leaving interrupted work `pending-rollback` and successful work
  `pending-verification`.
- [ ] Export and import a versioned tinderbox state bundle that pairs saved
  plans with the sanitized world file, profile/repository identities, complete
  Portage configuration layering (`make.conf`, `package.*`, profile parents and
  environment fragments), installed VDB metadata, selected ebuild/eclass and
  Manifest identities, toolchain/ABI contract and integrity fingerprints.
  Container import must reconstruct resolution without host paths or secrets,
  distinguish exact-machine from portable-baseline policy, reject drift before
  building and emit provenance linking every artifact to the bundle and plan.
  Use a deterministic tar archive as the stable interchange primitive, with a
  versioned canonical JSON manifest, normalized entry order/timestamps/modes/
  ownership, per-entry hashes and a digest of the logical uncompressed bundle.
  Compression is a transport encoding rather than bundle identity: default to
  zstd, support at least uncompressed tar, gzip and xz, detect codecs by magic
  rather than suffix, and permit recompression without changing logical bundle
  identity. Import must enforce schema and resource limits and reject absolute
  or traversing paths, device nodes, unsafe links, unexpected ownership and
  duplicate/conflicting entries. Signing and attestations cover the logical
  digest; compression metadata remains separately reportable.
  Define thin bundles containing sanitized configuration, identities, installed
  metadata, plans and hashes, plus full/offline bundles embedding the selected
  repository/profile/ebuild/eclass/Manifest material needed to reproduce the
  resolution. Keep the manifest capable of referencing content-addressed
  objects later so a tinderbox can deduplicate internally or transport bundles
  as OCI artifacts without replacing tar as the user-facing portable format.
- [ ] Make durable plans the inspectable contract between a local client and a
  remote tinderbox. Compare the locally resolved plan, exported state bundle,
  remote build plan and returned artifact attestations field by field; explain
  repository, profile, USE, ABI, dependency and toolchain divergence instead
  of reducing it to a cache miss. Allow a client to choose among explicitly
  compatible remote variants or a local source build, and make debugging a
  tinderbox reproducible from the exact named/content-addressed plans on both
  sides. The comparison format and core workflow must remain useful for a
  single self-hosted user and must not depend on a centralized Arise service.
  Preserve advisory estimate snapshots and their machine/sample provenance in
  the complete durable-plan digest for local-versus-tinderbox time and savings
  comparisons, but maintain a distinct execution-authorization digest over
  only behavior-affecting inputs so new timing history cannot stale approval.
- [ ] Design binary production as a reusable API for future tinderbox and
  stage4 automation, including deterministic output, provenance, signing,
  atomic repository publication and concurrent multi-package builds. Model
  per-machine build contracts (CHOST, CPU/ABI baseline, USE, CFLAGS/CXXFLAGS,
  toolchain and dependency snapshot) and allow one request population to be
  partitioned into exact-machine and explicitly declared lowest-common-
  denominator variants without silently weakening flags. Make artifact
  compatibility and provenance machine-checkable so clients can choose a
  remote binary or local source build using the same resolver action contract.
  Keep the protocol suitable for a community or paid multi-tenant service that
  offers binary-distribution convenience without erasing Gentoo customization:
  authenticated requests, signed results, private machine/profile policy,
  reproducible build attestations, quota/accounting, tenant-isolated builders,
  transparent cache sharing only for identical compatible contracts, and a
  self-hostable implementation with local-build fallback.
- [ ] Layer reproducible stage4 and cloud-image production on top of portable
  package state instead of overloading the package bundle itself. Define an
  image recipe that references an exact state bundle and durable plan, then adds
  kernel source/version/config and patches, module policy, initramfs generator
  and inputs, kernel command line, init system/services, filesystem/partition/
  mount layout, bootloader or firmware policy and image-specific configuration.
  Keep secrets and mutable instance identity outside the reproducible recipe.
  One recipe must be able to attest equivalent stage4 tar, raw disk, qcow2 and
  provider-specific cloud outputs through replaceable adapters, with every
  kernel, userspace, boot and filesystem input linked back to its plan/bundle.
  Package-only tinderbox workers must remain usable without understanding image
  assembly; stage4/cloud builders consume their signed outputs as a higher
  orchestration layer.
- [ ] Revisit the public name for the custom remote package-building service.
  Current provisional finalists are **Portage Speed Shop** (strongest Gentoo
  and tuned-performance character, but may imply official Portage affiliation),
  **The Binary Garage** (best independent, approachable service identity), and
  **Arise Package Customs** (strongest Arise-family and bespoke-package link).
  Preserve the core product metaphor regardless of the eventual name: a durable
  plan is a build sheet/order form that can be fulfilled locally or by a custom
  shop to the user's exact machine and package policy, then returned with a
  verifiable receipt. Continue brainstorming; no finalist is selected yet.
- [ ] Retain XPAK compatibility only where useful and tested.

### Tests

- [x] Cross-read packages produced by Portage and Arise.
- [x] Cross-install into isolated roots. Portage-produced zstd images and
  Arise-produced images are extracted through the same confined metadata-
  preserving path.
- [ ] Remote binhost fixture with Packages index updates.
- [ ] Round-trip installed package -> quickpkg-equivalent GPKG -> isolated ROOT
  and compare files, metadata and VDB state.
- [~] Round-trip regular files, symlinks, hardlinks, sparse files, unusual path
  names, numeric ownership, modes, timestamps, ACLs, xattrs and capabilities;
  compare the restored image and Portage-readable VDB byte-for-byte where the
  format is canonical and semantically everywhere else. Recovery-artifact
  coverage now exercises files, hardlinks, sparse allocation, modes,
  nanosecond timestamps and xattrs; GPKG cross-read and privileged
  ownership/ACL/capability lanes remain.
- [ ] Adversarial installed-state capture for missing, locally modified,
  type-changed, config-protected, preserved-library and foreign-owned paths.
  Policy must fail closed or preserve explicit evidence; it must never package
  an unexplained hybrid image.
- [ ] Atomicity matrix for capture startup, each archive/member publication
  boundary, disk-full/short-write/fsync failures, cancellation, process death
  and complete recovery-set publication. No live-root mutation may begin from
  a partial or corrupt recovery set.
- [ ] Restore complete multi-package recovery sets in dependency-safe/reverse
  transaction order, inject failure after every package boundary, recover the
  active journal and prove already committed/restored packages are neither
  lost nor silently repeated.
- [ ] Adversarial archive tests for absolute/traversing paths, unsafe links,
  duplicate/conflicting members, device nodes, decompression/resource bombs,
  malformed metadata, digest/signature failures and hostile Packages indexes.
  XPAK extraction now rejects the path/link/duplicate/device subset, including
  pre-existing destination symlinks, and bounds entries, expanded bytes,
  individual files, extended attributes and embedded metadata. Recovery-set
  signatures are mandatory. Packages-index hardening remains open.
- [ ] Concurrent capture, installation, repository publication, retention and
  garbage-collection tests; active or rollback-referenced artifacts must never
  be pruned.
- [ ] Reproducibility tests for repeated tinderbox builds and interrupted
  publication recovery.
- [ ] Corruption, signature and incompatible-USE tests.

Acceptance gate:

- Portage and Arise can consume each other's supported binary packages, and a
  fully captured pre-update package set can reconstruct an isolated ROOT/VDB
  after injected interruption without claiming restoration of external or
  package-unowned state.

## P10 — remaining emerge and maintenance behavior

- [ ] Replace low-information plan lines with emerge-density package records.
  The data-model and rendering contract in
  `docs/planning/PACKAGE_OUTPUT_UX_PLAN.md` covers artifact/action markers,
  selected and installed CPV/slot/subslot/repository identities, complete USE
  and USE_EXPAND state with provenance markers, rebuild causes, per-package
  download size, deterministic wrapping, colorless parity and versioned JSON.

- [ ] Centralize all terminal styling in one semantic presentation library.
  Replace physical helpers and mutable global color state with immutable
  renderers and named roles; prohibit raw ANSI escapes, terminal detection and
  color policy in command call sites. Support Portage `color.map`, explainable
  configuration provenance, accessibility themes and colorless informational
  parity as specified in
  [`docs/planning/COLOR_CONFIGURATION_PLAN.md`](docs/planning/COLOR_CONFIGURATION_PLAN.md).

- [~] Preserve emerge operator muscle memory alongside Arise's explicit
  subcommands. Bare atoms and sets now default to install resolution, and the
  familiar `-u/--update` path makes `arise -uDN @world` valid while explicit
  `install` and `update` remain available. Hermetic tests cover clustered short
  switches, option placement and default-command selection; a tagged live gate
  verifies every claimed advertised spelling against the installed
  `emerge --help`. Expand this into behavioral switch, action, exit-status and
  environment-variable parity matrices. Environment cases must verify effects
  for ROOT/SYSROOT/BROOT, PORTAGE_CONFIGROOT, PORTAGE_TMPDIR, DISTDIR, PKGDIR,
  FEATURES and command-scoped configuration rather than merely checking that a
  variable is accepted.

- [ ] Extract stable, independently versioned Go libraries from Arise's
  Portage-compatible core so other tooling can reuse atoms and dependency
  expressions, repository/VDB metadata, profile/config evaluation, immutable
  snapshots, Manifest/fetch verification, plan/conflict records and the phase
  protocol without importing the Arise CLI or mutable implementation details.
  Define compatibility policy, narrow interfaces, conformance fixtures and
  examples; keep orchestration, product policy and live mutation in Arise.
  GentooPM's package-manager-neutral object model and query API are audited in
  [`docs/audits/GENTOOPM_REFERENCE_AUDIT.md`](docs/audits/GENTOOPM_REFERENCE_AUDIT.md):
  preserve its useful portability and readable abstraction goals while avoiding
  a wrapper tied to Python PM internals.
- [ ] Publish a composable Bash runtime for ebuilds, eclasses and Gentoo tooling
  that replaces `set -e`/`set -euo pipefail` folklore with explicit checked-call,
  pipeline, cleanup/defer, typed diagnostic, stack-context and status-propagation
  helpers. Preserve expected nonzero statuses in conditionals and probes, never
  let a caller's shell options silently change library semantics, and make
  failures round-trip through Arise's structured phase protocol. Validate the
  library across functions, subshells, command substitutions, pipelines,
  traps/signals and nested sourcing on every supported Bash version.

- [ ] Implement accurate `emerge --info`-equivalent output.
- [x] Implement `arise maintain world --check` and `--fix` as the
  Portage-compatible counterpart to `emaint --check world` and
  `emaint --fix world`. Deterministic check and repair, versioned state-bound
  plans, direct explicit `--fix`, optional saved-plan approval, lock-time
  revalidation, mode/ownership-preserving atomic publication, idempotence, and
  captured live unavailable-entry parity are implemented. Check mode classifies
  malformed atoms, unavailable packages, duplicates, and installed-versus-
  uninstalled entries against one immutable repository/profile/VDB/world
  snapshot. Fix mode emits an exact state-bound repair plan, explains each
  action, takes the Portage world lock, revalidates the snapshot fingerprint,
  and publishes by mode-preserving atomic rename and fsync. Package moves
  preserve version, slot, repository and USE constraints; constrained entries
  are removed as redundant only when the same world set already contains the
  broader plain package atom. Unit, released-CLI and disposable-root Portage
  differential tests cover moves, masks, redundant constraints, concurrent
  changes, interrupted repair, idempotence and alternate
  ROOT/PORTAGE_CONFIGROOT isolation.
- [ ] Before public testing, implement a built-in, strictly read-only
  `arise bug-report` command backed by an isolated `internal/bugreport`
  collector and a versioned report schema. Generate a reviewable `report.md`
  plus structured JSON and selected durable logs; support latest-failure and
  explicit-package selection and deterministic `.tar.zst` export. Include the
  Arise build identity, sanitized invocation, approved-plan digest and summary,
  normalized failure/stage, package log, resume/journal state, relevant
  effective Portage policy, repository revisions, filesystem capacity/inodes,
  platform/toolchain details, conflict ownership evidence, scheduler/timing
  events and an `emerge --info` compatibility snapshot. Default-deny arbitrary
  environment/configuration values and redact usernames, home paths, hostnames,
  credentials, tokens, proxy URLs and private repository locations. Never
  upload or open an issue automatically: show the exact bundle for operator
  review first. Add hostile-fixture redaction tests, deterministic golden
  reports, partial/corrupt-state collection tests and schema compatibility
  tests. Ship an optional minimal shell collector under `libexec` only for
  catastrophic binary-startup failures; ordinary collection remains in Arise
  so its reader always matches the state formats it diagnoses.
- [ ] Ecosystem side quest: package the gawkextlib `gawk-json` extension in a
  local overlay, with an upstream-quality ebuild suitable for submission to
  Gentoo. Cover its gawk/RapidJSON requirements and test JSONL import, nested
  arrays/objects, null/boolean handling and serialization. Keep this amusing
  convenience strictly off Arise's critical path and do not make it a runtime
  dependency; ordinary JSONL must remain usable through many common tools.
- [ ] Match emerge's unknown-atom "did you mean" behavior. Generate
  deterministic typo/category suggestions from the same visible package
  snapshot used by resolution, distinguish unavailable/masked exact atoms from
  spelling mistakes, and never silently substitute a suggestion. Differential
  fixtures should cover misspelled package names, wrong categories, ambiguous
  names, versioned atoms and a no-useful-suggestion case.
- [ ] Implement package sets and list-sets behavior.
- [ ] Complete autounmask suggestions and atomic config writes.
- [x] Implement installed `pkg_config` execution. Lifecycle snapshots now
  persist phase functions, with a legacy installed-environment fallback, and
  fail explicitly when an installed package genuinely has no `pkg_config`.
- [~] Complete dispatch-conf-style recursive config management. Arise now has
  protected-tree and explicit-file discovery, stable candidate ordering,
  update/keep/skip/edit/merge/diff/quit decisions, masked and identical-file
  automation, safe three-way premerge, atomic confined archives and rotation,
  mixed file-type/symlink diffs, configured session/update hooks, metadata
  preservation, cancellation, and ROOT/PORTAGE_CONFIGROOT-aware adversarial
  tests. Interactive review matches Portage's full candidate-path ordering,
  clears the terminal before every comparison, and enables GNU diff color only
  for color-enabled terminal output while preserving custom diff commands and
  plain redirected output. Installed-Portage differential tests cover archive,
  symlink, and three-way-merge semantics. Remaining parity work includes
  explicit session rollback/recovery and full command-level differentials
  against Portage's dispatch-conf and etc-update in a disposable ROOT. Cover
  etc-update's preen and automatic modes there. Run the reference dispatch-conf
  inside a chroot or mount-isolated root because it has no root-selection CLI.
  Never use the live host configuration as a behavioral fixture.
- [ ] Expose config-protection and dispatch decisions as a headless Go library
  with immutable candidate/diff records, explicit decision requests, validated
  apply plans, archive/rollback operations and event streams. Build a polished
  Charmbracelet-based dispatch-conf TUI as a separate reference consumer of
  that API—supporting keyboard-driven review, side-by-side/unified diffs,
  filtering and resumable sessions—without duplicating protected-file policy
  or granting the UI a path around journaling and apply validation.
- [ ] Wire preserved-rebuild and revdep-rebuild through the safe planner/executor.
- [ ] Supplant `perl-cleaner`: detect Perl subslot/ABI transitions, stale module
  files and packages linked to obsolete libperl instances; produce one
  resolver-backed pretend/rebuild plan with explicit reasons.
- [ ] Add a Python repair/cleaner workflow: detect stale interpreter targets,
  invalid shebangs, orphaned site-packages, extension modules linked against
  removed libpython ABIs and packages whose installed PYTHON_TARGETS no longer
  satisfy current profile policy. Exploit Arise's independence from Portage's
  Python runtime to break interpreter-transition deadlocks: stage replacement
  interpreters and consumers first, run import/linkage/shebang probes, and only
  permit obsolete interpreter removal after whole-state verification. The
  bootstrap advantage must improve recovery capability, not weaken graph or
  filesystem safety.
- [ ] Make both cleaner workflows usable when Portage's Python environment is
  broken, with read-only audit, structured output, pretend, bounded repair and
  resumable execution modes.
- [ ] Differential-test Perl repair plans against `perl-cleaner`, and validate
  Python repair plans against VDB ownership, linkage and interpreter import
  probes in disposable broken-root fixtures.
- [ ] Complete news relevance filtering.
- [ ] Add a migration-advice engine that correlates relevant Gentoo news items
  with the active profile, installed packages, selected versions and proposed
  transaction before risky upgrades.
- [ ] Define a versioned, machine-readable migration-action format for trusted
  repository metadata (package replacements, set changes, config prerequisites,
  cleaner/rebuild passes and ordered pre/post checks); never execute commands
  extracted from natural-language news prose.
- [ ] Convert recognized advice into an explainable pretend plan with source,
  applicability evidence, ordering, rollback boundary and explicit approval;
  preserve unrecognized prose as a blocking or advisory notice according to
  repository policy.
- [ ] Cache completed migration action IDs transactionally so advice is neither
  skipped nor repeated, while remaining portable across Arise and Portage use.
- [ ] Test stale, superseded, conflicting, malformed and malicious advice plus
  interrupted migrations in disposable ROOT fixtures.
- [ ] Audit every emerge short/long flag for semantics, not only parsing.
- [~] Add stable machine-readable plan and event APIs. Pretend install/update
  plans now have a versioned JSON schema containing timings, actions, effective
  USE, partial conflicts, warnings and errors; event/progress and audit schemas
  remain.

## Better-than-emerge targets

These are product goals, not substitutes for correctness:

- [ ] Sub-second warm planning for common single-package operations.
- [ ] Order-of-magnitude faster warm search and installed-state queries.
- [ ] Order-of-magnitude faster incremental repository ingestion than a full
  metadata rebuild.
- [ ] Materially faster warm `@world` planning on the same state snapshot.
- [ ] Near-zero repeated parsing: immutable evaluated metadata is reused across
  searches, explanations and resolver variants.
- [!] Replace eager full-repository plan construction with an immutable compact
  resolver snapshot and lazy CPV/dependency loading. Parallel metadata decoding
  reduced the live Signal plan from 2.53 s to 1.50 s. Effective USE, mask and
  keyword policy are now cached per immutable CPV: on the broken-world
  complete-graph corpus this reduced resolution from 31.8 s to a repeatable
  9.54-9.58 s without changing the plan (search 19.15→6.10 s, verification
  10.45→1.86 s). The current Portage run is 17.0 s, but plan equivalence remains
  a prerequisite for publishing that directional advantage.
- [ ] Incremental index updates after sync instead of full rebuilds.
- [ ] Explain mode showing why each candidate was accepted or rejected.
- [~] Structured JSON plan, conflict, progress and audit output. Plan actions
  and structured conflict details are implemented; progress and audit streams
  remain.
- [~] Deterministic concurrent logs that remain readable. P4 now treats
  Portage-compatible durable package logs and elog delivery as a live-mutation
  blocker. Beyond parity, add a versioned structured event log with operation,
  package, phase, job, stream and causal-error identities; deterministic views;
  redaction/export for bug reports; indexed querying; live TUI attachment; and
  correlation with resolver decisions, filesystem journal records and rollback.
  The structured store must complement durable plain-text logs rather than make
  recovery depend on a database reader.
- [ ] Content-addressed fetch/build cache with verified reuse.
- [ ] Safe operation preview including exact filesystem/config/VDB mutations.
- [~] First-class rollback and crash recovery. Package merge/unmerge has a
  durable undo journal, process-death coverage, active-operation recovery and
  retry through the public command path. Extend the same operator-facing
  recovery contract to every mutation class and whole-operation world updates.
  Whole-operation rollback will use verified Btrfs, OpenZFS or LVM snapshots;
  Arise will not build an immutable package store, generation-symlink ROOT or
  second path garbage collector. OverlayFS remains a rehearsal/lifecycle-delta
  mechanism rather than a post-commit rollback claim.
- [ ] Add pre-update recovery binpkgs when Arise subsumes `quickpkg`. Before an
  approved replacement/removal transaction, capture the complete affected
  installed package set with exact VDB identity, file metadata/integrity,
  locally modified or missing-path evidence, ROOT/plan fingerprints and bounded
  recovery-set retention. Restore through normal journaled transactions after
  re-resolution and separate approval for drift. This is portable package-set
  reconstruction, not atomic rollback of lifecycle, external or package-unowned
  state.
- [ ] Plan diff: show what changed since the last sync or previous solution.
- [ ] Resolver trace that can be attached to bug reports without private data.
- [ ] Counterfactual planning: compare profile, USE, keyword, license, repository
  or package-version choices without editing live configuration, and explain
  the smallest policy change that produces each alternative plan.
- [ ] Upgrade risk report before approval: identify critical-path packages,
  ABI/subslot transitions, removals, config-file changes, preserved libraries,
  boot/session/network impact and the transaction's last safe rollback point.
- [ ] Portage configuration doctor: lint stale or shadowed package.* atoms,
  contradictory USE policy, obsolete targets, unknown flags, duplicate rules,
  repository drift and settings that no installed/visible package consumes.
- [ ] Maintenance cockpit with evidence-backed priorities: combine news,
  security advisories, preserved rebuilds, broken linkage, stale interpreters,
  depclean candidates and pending config updates into one explainable plan,
  without turning advisory findings into automatic mutations.
- [ ] Add an opt-in salvage planner (tentatively `--salvage-plan`, with
  `--keep-trying` considered as an alias) for damaged large sets. Partition the
  request into deterministic independently verifiable transaction components,
  emit a maximal safe subset plus the excluded conflict frontier, and require a
  separately saved/approved plan for every executable chunk. Re-resolve against
  actual state after each committed chunk; never implement this by slicing an
  invalid global plan or changing default `--keep-going` semantics. The same
  component identities should support remote tinderbox distribution. Explore
  named, independently reproducible salvage scenarios that share one frozen
  input snapshot and emit comparable durable plans:
  - aggressive upgrade: prefer the highest permitted versions, explore
    explicitly proposed unmask/keyword changes, and retire obsolete slots;
  - conservative/stable: preserve installed slots and versions where possible,
    allowing bounded minor downgrades when they repair older constraints;
  - optional USE shuffle: search only policy-classified non-critical flags such
    as `test`, `doc` or `minimal`, never ABI/security/feature-critical flags by
    an unreviewed heuristic;
  - autounmask: produce the smallest proposed `package.use`,
    `package.accept_keywords` and `package.unmask` delta needed for a verified
    graph.
  These are explicit strategies, not silent fallback. They must not write live
  configuration while searching; each result records its policy/config delta,
  excluded frontier and tradeoffs, passes whole-state verification, and needs
  separate plan approval before any mutation.
- [ ] Reproducible privacy-reviewed support bundle containing configuration and
  repository fingerprints, normalized plan/conflicts, resolver trace and
  relevant VDB metadata; redact paths, hostnames, mirrors and secrets by
  default and make the bundle diffable across machines.
- [ ] Transaction rehearsal in a disposable ROOT or overlay snapshot, including
  lifecycle probes and post-state verification, before offering the identical
  journaled plan for approval on the live root.
- [~] Fast versionless installed-atom output and strong eix-style search.
  Search now supports case-insensitive shell-style category/package globs in
  the normal and names-only paths; `app-editors/*` returns the same complete
  97-package live set as eix. Full CP queries now match the combined
  category/package identity rather than incorrectly searching each component
  for the slash-containing string; live `dev-lang/ruby` returns the same
  four-slot version set as eix. Arise deliberately has no implicit terminal
  result cap or pager requirement: output is unlimited unless the caller
  explicitly requests a limit. Remaining eix-style query forms and performance
  gates keep this item open.
- [x] Immutable versioned package-name sidecar with atomic replacement,
  checksums, exact lookup, substring lookup, and Badger fallback.
- [ ] Add trigram name postings only if a correctness-gated broad or no-result
  workload falls below the eix speed floor; the current contiguous index wins.
- [ ] Add category membership postings when category-only benchmarks show that
  decoding package metadata is material.
- [ ] Add IUSE flag postings for `--has-use`/USE searches, validated against
  eix result sets before timing them.
- [ ] Add forward and reverse dependency postings for dependency search and
  resolver candidate discovery; preserve dependency-class and conditional-use
  information needed for correctness.
- [ ] Add a compact installed-state snapshot keyed by CP/slot/repository, with
  VDB identity/invalidation so stale installation state is never returned.
- [ ] Consider version/slot/keyword postings only after their workloads expose
  a measurable metadata-decoding bottleneck.
- [~] One static recovery binary capable of inspecting and repairing state when
  Python/Portage is unusable. Release artifacts are built with `CGO_ENABLED=0`,
  static linkage is packaging-gated, and the recovery command can inspect and
  repair journal state. Preserved Bash/tool drills, log correlation and broader
  state repair remain.
- [x] Preserve the single self-contained Go-tool model: no Python runtime or
  cgo database dependency. Binary size and cold startup remain numeric P0A
  budgets rather than architectural dependencies.

These targets must be converted into numeric budgets after the P0A baseline.
The budgets should be ambitious enough that “slightly faster emerge” fails.

## Test program

### Required layers

1. **Unit tests** for parsers, reducers and algorithms.
2. **Property/fuzz tests** for atoms, versions, expressions and transactions.
3. **Golden fixtures** for profiles, repositories, VDB and plans.
4. **Differential tests** against Portage on a live Gentoo host.
5. **Isolated ROOT integration tests** for mutation without risking the host.
6. **Cross-compatibility tests** where Portage reads Arise state and vice versa.
7. **Failure-injection tests** at every persistent-state boundary.
8. **Race and stress tests** for concurrent subsystems.
9. **Benchmarks with correctness assertions** and stored regression thresholds.
10. **CLI/API contract tests** for exit status, output and JSON schemas.

### Initial real-package corpus

The corpus should cover distinct semantics rather than only popular packages:

- `net-im/signal-desktop-bin` — binary package, ABI and desktop dependency graph.
- `www-client/firefox` — slots, L10N, large USE surface and updates.
- `sys-apps/portage` — Python/eclass-heavy core package.
- `sys-libs/glibc` — critical library and subslot behavior.
- `dev-lang/python` — slots and implementation rebuilds.
- `sys-devel/gcc` — slots, bootstrap and large builds.

### Configuration-diversity fixtures

- OpenRC and systemd profiles, including elogind and dbus-broker alternatives.
- sudo and doas administration stacks; tests must not assume either or root via sudo.
- multilib, no-multilib and ABI_X86 parent/child USE-dependency propagation.
- desktop and server profiles with minimal and expansive global USE sets.
- package manager state readable by an unprivileged user after privileged indexing.
- a trivial local ebuild — lifecycle and transaction baseline.
- a live `9999` ebuild — VCS behavior.
- a kernel module — module-rebuild behavior.
- a package with CONFIG_PROTECT files.
- packages with blockers, any-of deps, REQUIRED_USE and license groups.

### CI gates

- [ ] `gofmt` and `git diff --check` clean.
- [ ] `go vet ./...` clean.
- [ ] Unit and contract tests pass.
- [ ] Race tests pass for concurrent packages.
- [ ] Fuzz smoke corpus passes.
- [ ] Isolated ROOT smoke cycle passes.
- [ ] Differential parity report is generated and regressions fail CI.
- [ ] Benchmarks flag material regressions without hiding correctness failures.
- [ ] Same-snapshot Portage comparisons meet the current milestone's speed budget.

## P11 — musl platform support (later)

- [ ] Make libc identity a first-class platform dimension sourced from the
  active Gentoo profile/CHOST and effective `elibc_*` policy, never inferred
  from the development host or a hard-coded loader pathname.
- [ ] Add an amd64 musl resolver/configuration corpus covering `sys-libs/musl`,
  libc virtuals, profile masks/forces, USE conditionals, dependency closure and
  exact normalized comparison with Portage. Extend to aarch64 after the first
  architecture is exact.
- [ ] Audit ELF and preserved-library handling for musl interpreter names,
  loader/library search behavior, static PIE, merged-/usr layouts and the
  absence or differing behavior of glibc-specific `ldconfig`, `ldd`,
  `ld.so.conf` and `glibc-hwcaps`. Keep the existing glibc-hwcaps traversal an
  explicitly glibc-only extension of otherwise libc-neutral discovery.
- [ ] Certify the phase ABI and representative package corpus in a real musl
  container/chroot, including configure/CMake/Meson libc detection, toolchain
  variables, sandboxing, install images, VDB metadata and lifecycle hooks.
- [ ] Build and execute the static recovery binary on musl, and ensure portable
  state/tinderbox requests encode target libc and ABI so glibc and musl plans or
  binary packages can never be confused.
- [ ] Add CI lanes for pure-Go unit tests under musl and privileged integration
  tests against a pinned Gentoo musl stage. Unsupported libc/architecture
  combinations must fail with a capability diagnostic rather than silently
  applying glibc assumptions.

Acceptance gate:

- An amd64 musl `@system` plan matches Portage on a frozen state snapshot,
  representative source installs produce equivalent images/VDB state, ELF and
  preserved-library audits pass without glibc tools, and recovery works from
  the static Arise artifact.

## Milestones

### M1 — trustworthy planner

P0A through P3 complete. Arise can produce Portage-equivalent pretend plans but
does not mutate the host. Warm common plans are substantially faster than
emerge, and the stored benchmark report proves result equivalence.

### M2 — isolated-root installer

P4 through P8 complete for a declared EAPI/package subset. Arise safely manages
a disposable ROOT with recovery. Independent work is overlapped by the DAG
scheduler, duplicated work is avoided, and end-to-end timings beat emerge for
the declared corpus.

### M3 — interoperable daily driver

P9 and the essential P10 items complete. Binary packages and VDB state are
cross-compatible with Portage, the real-package corpus passes, and everyday
query/planning/update workflows meet the decisive-speedup budgets.

### M4 — emerge competitor

Parity corpus is broad, failures are explainable and recoverable, performance
budgets materially surpass emerge and are enforced in CI, and better-than-emerge
features are stable APIs rather than demonstrations.
