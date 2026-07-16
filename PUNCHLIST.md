# Arise Punch List

This is the ordered delivery plan for making Arise a safe, fast, real
competitor to `emerge`. Items are ordered by dependency, not by apparent ease.

Complete feature parity and decisive performance are joint requirements.
Correctness work must preserve the indexed, immutable, concurrent architecture
that makes large speedups possible. Performance work may never weaken
compatibility, determinism, safety, or recovery.

Status markers:

- `[ ]` not started
- `[~]` partial or under active work
- `[x]` acceptance criteria satisfied
- `[!]` release blocker

## Rules of engagement

- [ ] A parsed flag is not an implemented feature.
- [ ] Every mutating feature has pretend, failure, interruption, and recovery tests.
- [ ] Filesystem mutations require a journal or an atomic replacement strategy.
- [ ] Repository/VDB/profile snapshots are immutable during one resolution.
- [ ] Concurrent output and results remain deterministic.
- [ ] Every parity claim links to a differential or end-to-end test.
- [ ] Performance work includes correctness checks and benchmark baselines.
- [ ] Every milestone has a same-snapshot comparison against Portage.
- [ ] No milestone ships on parity alone; it must meet its performance gate.
- [ ] No benchmark is valid unless result equivalence is asserted.
- [ ] Equivalent-but-slower is a blocking bug. Real workloads require at least
  1.0x median speedup, and milestone budgets should be significantly higher
  wherever the architecture provides leverage.
- [ ] Unsupported behavior fails explicitly; it must never silently no-op.

## P0A — performance laboratory and budgets

- [x] Build a reproducible benchmark harness that runs Arise and Portage on the
  same immutable repository/profile/VDB snapshot.
- [~] Record hardware, kernel, filesystem, storage, Go, Python and reference-tool versions.
- [ ] Separate cold-cache, warm-cache and incremental-operation measurements.
- [ ] Measure wall time, CPU time, peak RSS, bytes read/written and process count.
- [x] Store machine-readable benchmark results and equivalence verdicts.
- [~] Establish representative small, medium and full-world workloads.
- [x] Publish a full emerge/eix comparison matrix covering current and planned tasks.
- [x] Add statistical repetitions, median and p95 reporting.
- [ ] Define regression budgets before optimizing each subsystem.
- [ ] Fail performance CI on material regressions beyond the agreed noise band.
- [ ] Keep microbenchmarks, but gate releases on end-to-end workloads.

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

- [!] Mark install/update/uninstall/depclean/prune as experimental until their
  execution paths meet the gates below.
- [x] Remove or qualify “full emerge parity” claims in user documentation.
- [!] Convert unsupported ebuild phases and FEATURES from silent success to
  explicit errors.
- [x] Fix all current `go vet` findings; keep vet clean in CI.
- [ ] Add a schema version and application version to persistent state.
- [ ] Add a global operation lock compatible with Portage's live-system safety
  expectations.

Acceptance gate:

- Commands cannot report success for work they did not perform.
- Documentation accurately distinguishes usable, experimental, and planned behavior.

## P1 — correct package-state model

### Repository metadata

- [!] Replace CP-only records with immutable repository+CPV records.
- [ ] Preserve repository name, path, priority, masters, EAPI and overlay order.
- [ ] Add secondary indexes for CP, slot, repository and visibility inputs.
- [ ] Make concurrent ingestion deterministic regardless of goroutine ordering.
- [ ] Support incremental sync/index transactions and stale-record removal.
- [ ] Detect md5-cache changes using digest/mtime without trusting them as package state.

### Installed state

- [!] Create a separate VDB ingestion model.
- [ ] Preserve installed CPV, slot/subslot, repository, USE, IUSE, dependencies,
  build time, build ID, COUNTER, EAPI and CONTENTS.
- [ ] Support multiple installed slots and versions.
- [ ] Reconcile cache state with filesystem truth on open and after operations.

### Tests

- [ ] Property test: ingestion order cannot change indexed results.
- [ ] Fixture: multiple versions of one CP all survive indexing.
- [ ] Fixture: same CPV in multiple repositories follows repos.conf priority.
- [ ] Fixture: repository records are never marked installed without VDB evidence.
- [ ] Crash test: interrupted index leaves the previous snapshot readable.

Acceptance gate:

- A state dump lists exactly the same available CPVs and installed CPVs as
  Portage for the test machine and fixture repositories.

## P2 — Portage configuration and profile evaluation

- [!] Wire the active profile into production state construction.
- [ ] Implement profile parents and repository masters deterministically.
- [ ] Implement make.defaults stacking and variable expansion needed by package policy.
- [ ] Implement USE_ORDER.
- [ ] Implement USE_EXPAND, USE_EXPAND_HIDDEN and implicit expansion flags.
- [ ] Implement use.mask/use.force and stable/package variants.
- [ ] Implement package.use matching using full Gentoo atoms, ordering and removal syntax.
- [ ] Implement package.mask/unmask and repository/profile masks with reasons.
- [ ] Implement ACCEPT_KEYWORDS and package.accept_keywords accurately.
- [ ] Implement license groups, expressions, ACCEPT_LICENSE and package.license.
- [ ] Implement package.env and supported per-package environment layering.
- [ ] Implement package.provided with version-aware matching.
- [ ] Build @system from the active profile.

### Tests

- [ ] Differential test effective USE against Portage for at least 100 real CPVs.
- [ ] Differential test visibility/best-visible and masking reason.
- [ ] Matrix tests for profile parent precedence and package.* directories.
- [ ] Regression fixture for ABI_X86 and `abi_x86_32?` dependencies.
- [ ] Regression fixture for IUSE `+flag` and `-flag` defaults.
- [ ] Property test that configuration reduction is deterministic.

Acceptance gate:

- Arise agrees with Portage on visible candidates and effective USE for the
  selected real-world corpus.

## P3 — dependency expression and resolver correctness

- [ ] Preserve complete per-version dependency expressions in the graph.
- [ ] Implement EAPI-correct DEPEND/RDEPEND/BDEPEND/IDEPEND/PDEPEND behavior.
- [ ] Implement complete atom semantics, slots, subslots and repository constraints.
- [ ] Implement USE dependency defaults and conditional forms.
- [ ] Implement REQUIRED_USE as a real boolean/cardinality expression evaluator.
- [ ] Implement blockers and replacement/unmerge ordering.
- [ ] Implement virtual/provider selection and provider preference.
- [ ] Implement any-of groups with installed and minimal-change preferences.
- [ ] Implement circular dependency detection and useful diagnostics.
- [ ] Implement slot conflict explanations.
- [ ] Implement subslot rebuilds and complete-graph behavior.
- [ ] Implement changed-use, newuse and changed-deps from installed metadata.
- [ ] Implement real backtracking with bounded decision history.
- [ ] Produce structured conflict causes for autounmask and human output.
- [ ] Stop forcing `Deep` for ordinary installs; model new-dependency traversal correctly.

### Tests

- [ ] Differential plan tests against `emerge -p` on a curated corpus.
- [ ] Golden tests for blockers, slots, subslots, virtuals and any-of groups.
- [ ] Property tests for topological ordering and solution satisfaction.
- [ ] Mutation tests that remove or invert constraints and must fail.
- [ ] Fuzz atom and dependency expression parsers with round-trip invariants.
- [ ] Regression test for `net-im/signal-desktop-bin` on the laptop snapshot.

Acceptance gate:

- Arise's candidate set, closure and action intent match emerge for the corpus,
  except for explicitly documented and tested improvements.

## P4 — real EAPI/ebuild execution ABI

- [!] Design a versioned Go-to-Bash execution protocol.
- [!] Source the chosen ebuild and inherited eclasses in an isolated environment.
- [ ] Implement phase discovery and EAPI default phases.
- [ ] Implement required environment variables and directory layout.
- [ ] Implement a minimum complete helper ABI for current supported EAPIs.
- [ ] Execute pkg_setup, source phases, pkg_preinst/postinst and pkg_prerm/postrm.
- [ ] Honor RESTRICT and PROPERTIES.
- [ ] Capture structured logs, phase events, qa notices and exit status.
- [ ] Preserve the static Go control plane when Python is unavailable.
- [ ] Declare supported EAPIs and reject unsupported EAPIs before mutation.

### Tests

- [ ] Synthetic ebuild fixture per phase and supported EAPI.
- [ ] Eclass inheritance and exported phase fixtures.
- [ ] Helper ABI contract tests.
- [ ] Environment snapshot comparisons with Portage.
- [ ] Build representative packages: trivial, autotools, cmake, meson, Go,
  Python, Rust, kernel module, binary-only and config-protected.
- [ ] Failure injection at every phase.

Acceptance gate:

- Representative packages produce equivalent image trees and lifecycle effects
  under Portage and Arise.

## P5 — fetch and verification

- [ ] Reuse DISTDIR atomically and avoid duplicate concurrent downloads.
- [ ] Implement Manifest digest and size verification.
- [ ] Implement GENTOO_MIRRORS and `mirror://` expansion.
- [ ] Implement ordered URI fallback and rename syntax.
- [ ] Implement supported fetch-command overrides and protocols.
- [ ] Implement partial download resume safely.
- [ ] Implement RESTRICT=fetch and pkg_nofetch behavior.
- [ ] Integrate VCS fetchers through eclasses/execution ABI.
- [ ] Deduplicate network work across concurrent package builds.

### Tests

- [ ] Local HTTP fixture for redirects, ranges, failures and fallback.
- [ ] Digest mismatch and corrupted-cache tests.
- [ ] Concurrent request deduplication test.
- [ ] Offline build using a complete DISTDIR.

Acceptance gate:

- Fetch-only and normal builds never consume unverified distfiles when a
  Manifest digest is available.

## P6 — transactional merge and unmerge

- [!] Design and implement an operation journal.
- [!] Run collision/ownership validation before live-root mutation.
- [ ] Implement CONFIG_PROTECT and CONFIG_PROTECT_MASK.
- [ ] Preserve modes, ownership, timestamps, symlinks, hardlinks, xattrs, ACLs
  and file capabilities.
- [ ] Generate complete Portage-readable VDB entries.
- [ ] Implement replacement ordering and safe old-version unmerge.
- [ ] Implement collision-protect and protect-owned.
- [ ] Integrate preserve-libs and rebuild records.
- [ ] Commit VDB and world changes atomically with filesystem state.
- [ ] Implement rollback/recovery after interruption or process death.
- [ ] Serialize or conflict-partition live merges.

### Tests

- [ ] Merge/unmerge round-trip property tests.
- [ ] Compare image root and VDB with Portage fixtures.
- [ ] CONFIG_PROTECT behavioral matrix.
- [ ] File ownership and cross-package collision tests.
- [ ] Kill process at every journal boundary and recover.
- [ ] Concurrent non-conflicting and conflicting merge tests.

Acceptance gate:

- An interrupted operation is either fully committed or recoverable, and
  Portage can read and manage the resulting VDB state.

## P7 — dependency-aware concurrent scheduler

- [ ] Replace FIFO worker distribution with a DAG ready queue.
- [ ] Release dependents only after successful dependency completion.
- [ ] Propagate failures and implement keep-going by independent subgraph.
- [ ] Apply load-average backpressure in production workers.
- [ ] Model CPU, memory, network and merge resources separately.
- [ ] Support fetch-ahead while respecting build and merge ordering.
- [ ] Make output deterministic with per-package event streams.
- [ ] Persist completed nodes for resume.
- [ ] Avoid rebuilding successful packages after a downstream failure.

### Tests

- [ ] Assert no dependent begins before every required predecessor succeeds.
- [ ] Assert independent branches run concurrently.
- [ ] Deterministic event/output test across repeated runs.
- [ ] Load-average and cancellation tests.
- [ ] Race detector and stress tests with hundreds of synthetic nodes.

Acceptance gate:

- Scheduler is dependency-correct, deterministic, resumable and measurably
  faster than serial execution on parallel graphs.

## P8 — wire install, update and removal end to end

- [!] Connect resolved plans to fetch/build/binpkg/merge/unmerge execution.
- [ ] Correctly implement pretend, ask, fetchonly and buildpkgonly.
- [ ] Update world only for successful explicit installs and respect oneshot.
- [ ] Mark resume nodes complete after transaction commit.
- [ ] Implement update @world and package sets.
- [ ] Implement uninstall with reverse-dependency safety.
- [ ] Implement depclean and prune execution with confirmation and journaling.
- [ ] Implement deselect interactions and preserved rebuild scheduling.
- [ ] Handle signals and cancellation without corrupting state.

### Tests

- [ ] End-to-end install into an isolated ROOT.
- [ ] Upgrade, downgrade, reinstall, slot coexistence and replacement tests.
- [ ] End-to-end uninstall/depclean/prune tests.
- [ ] Resume after injected fetch, build and merge failures.
- [ ] Verify pretend performs zero mutations.

Acceptance gate:

- Arise can safely manage a disposable Gentoo ROOT through install, update,
  removal, interruption and recovery cycles.

## P9 — modern binary package support

- [ ] Implement current Gentoo GPKG reading and writing.
- [ ] Parse and generate Packages indexes.
- [ ] Support build IDs and multiple package instances.
- [ ] Verify digests and signatures according to policy.
- [ ] Implement local and remote candidate selection.
- [ ] Implement binpkg USE/config compatibility.
- [ ] Wire `-k`, `-K`, `-g`, `-G`, `-b`, and `-B` end to end.
- [ ] Retain XPAK compatibility only where useful and tested.

### Tests

- [ ] Cross-read packages produced by Portage and Arise.
- [ ] Cross-install into isolated roots.
- [ ] Remote binhost fixture with Packages index updates.
- [ ] Corruption, signature and incompatible-USE tests.

Acceptance gate:

- Portage and Arise can consume each other's supported binary packages.

## P10 — remaining emerge and maintenance behavior

- [ ] Implement accurate `emerge --info`-equivalent output.
- [ ] Implement package sets and list-sets behavior.
- [ ] Complete autounmask suggestions and atomic config writes.
- [ ] Implement pkg_config execution.
- [ ] Complete dispatch-conf-style recursive config management.
- [ ] Wire preserved-rebuild and revdep-rebuild through the safe planner/executor.
- [ ] Complete news relevance filtering.
- [ ] Audit every emerge short/long flag for semantics, not only parsing.
- [ ] Add stable machine-readable plan and event APIs.

## Better-than-emerge targets

These are product goals, not substitutes for correctness:

- [ ] Sub-second warm planning for common single-package operations.
- [ ] Order-of-magnitude faster warm search and installed-state queries.
- [ ] Order-of-magnitude faster incremental repository ingestion than a full
  metadata rebuild.
- [ ] Materially faster warm `@world` planning on the same state snapshot.
- [ ] Near-zero repeated parsing: immutable evaluated metadata is reused across
  searches, explanations and resolver variants.
- [ ] Incremental index updates after sync instead of full rebuilds.
- [ ] Explain mode showing why each candidate was accepted or rejected.
- [ ] Structured JSON plan, conflict, progress and audit output.
- [ ] Deterministic concurrent logs that remain readable.
- [ ] Content-addressed fetch/build cache with verified reuse.
- [ ] Safe operation preview including exact filesystem/config/VDB mutations.
- [ ] First-class rollback and crash recovery.
- [ ] Plan diff: show what changed since the last sync or previous solution.
- [ ] Resolver trace that can be attached to bug reports without private data.
- [ ] Fast versionless installed-atom output and strong eix-style search.
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
- [ ] One static recovery binary capable of inspecting and repairing state when
  Python/Portage is unusable.
- [ ] Preserve the single self-contained Go-tool model: no Python runtime or
  cgo database dependency; track binary size and cold startup as budgets.

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
