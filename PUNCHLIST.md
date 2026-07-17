# Arise Punch List

This is the ordered delivery plan for making Arise a safe, fast, real
competitor to `emerge`. P-numbers are stable milestone identifiers, not a
topological ordering; the dependency graph below governs execution order.

Complete feature parity and decisive performance are joint requirements.
Correctness work must preserve the indexed, immutable, concurrent architecture
that makes large speedups possible. Performance work may never weaken
compatibility, determinism, safety, or recovery.

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

Current critical path: P0B cleanup -> portable P2/P3 fixtures -> mandatory P3
verification -> P4 protocol/environment -> P5 delivery completion -> P4 helper
ABI -> P6 isolated-ROOT journal and recovery -> P7/P8 integration.

Status markers:

- `[ ]` not started
- `[~]` partial or under active work
- `[x]` acceptance criteria satisfied
- `[!]` release blocker

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
- [ ] Separate cold-cache, warm-cache and incremental-operation measurements.
- [ ] Measure wall time, CPU time, peak RSS, bytes read/written and process count.
- [x] Store machine-readable benchmark results and equivalence verdicts.
- [~] Establish representative small, medium and full-world workloads.
- [x] Publish a full emerge/eix comparison matrix covering current and planned tasks.
- [x] Add statistical repetitions, median and p95 reporting.
- [ ] Define regression budgets before optimizing each subsystem.
- [ ] Fail performance CI on material regressions beyond the agreed noise band.
- [ ] Keep microbenchmarks, but gate releases on end-to-end workloads.
- [~] Track test coverage in separate deterministic-core, network/integration
  and benchmark-harness lanes. Whole-tree instrumentation gives the fast core
  lane a 68.4% statement baseline while counting excluded production code as
  uncovered. The benchmark lane passes in 119.4 seconds, covers 66.5% of its
  harness package and exercises 13.5% of the whole production tree. Network
  coverage is explicitly sandbox-blocked. Safety-critical thresholds remain
  to define after per-file risk classification.
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

- [x] Mark install/update/uninstall/depclean/prune as experimental until their
  execution paths meet the gates below. Install/update still fail explicitly
  after a successful non-pretend plan instead of returning false success;
  source fetch-only may populate DISTDIR through the Manifest-verified P5 path
  while binary acquisition and unsupported transports fail explicitly. Pretend
  remains available for real-machine validation. Uninstall,
  depclean and prune likewise produce read-only proposals in pretend mode and
  explicitly refuse non-pretend execution pending P6 journaling and safety.
  Preserved/reverse-dependency rebuilds and audit fixes can scan in pretend
  mode but can no longer bypass the P4/P6 execution gate.
- [x] Remove or qualify “full emerge parity” claims in user documentation.
- [~] Convert unsupported ebuild phases and FEATURES from silent success to
  explicit errors. Unknown phases and every unimplemented pkg_* lifecycle phase
  now return a typed execution-ABI error instead of succeeding. The legacy
  runner rejects every enabled known or unknown feature outside its explicit
  supported set instead of logging sandbox stubs. The legacy synthetic
  src_prepare placeholder remains the sole deliberate exception because the
  prototype rebuild assumes an EAPI default phase sequence; it must be replaced
  by phase discovery plus real default src_prepare/eapply_user in the versioned
  Bash ABI. Production install/update remain hard-gated meanwhile.
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

Current live safety check: a copied binary produced the exact one-action Signal
pretend plan; the equivalent non-pretend command exited 1 at the execution gate.
Before/after fingerprints of every VDB file and `/var/lib/portage/world` were
identical, proving the gated invocation performed no package-state mutation.

## P1 — correct package-state model

### Repository metadata

- [x] Replace CP-only records with immutable repository+CPV records.
- [x] Preserve repository name, path, priority, masters, EAPI and overlay order.
- [x] Add secondary indexes for CP, slot, repository and visibility inputs.
- [x] Make concurrent ingestion deterministic regardless of goroutine ordering.
- [x] Support incremental sync/index transactions and stale-record removal.
- [x] Detect md5-cache changes using digest/mtime without trusting them as package state.
- [x] Index repositories without a pre-generated metadata/md5-cache, marking
  statically discovered records incomplete and unsafe for resolution.

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
  snapshot fingerprints are recorded in `misc/P2_PARITY_EVIDENCE_2026-07-17.json`.
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

### Live plan-equivalence campaign

The potentially superior live plan is tracked as a hypothesis, with threats to
validity and promotion criteria, in
[`misc/PLAN_COMPLETENESS_VALIDATION.md`](misc/PLAN_COMPLETENESS_VALIDATION.md).

- [x] Add normalized Arise/emerge pretend-plan parsers and structured action-set
  differences preserving CPV, slot/subslot, repository, action and USE groups.
- [x] Add a command that runs equivalent Arise and emerge plans and reports
  only-in-Arise, only-in-emerge, version, location and action differences.
- [x] Make that comparator consume Arise's versioned JSON plan rather than
  color- and wording-sensitive terminal output.
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
  Fixing deep installed-dependency
  promotion moved `@system --deep` from 13/131 to 129/131 actions, with 39
  normalized residual differences concentrated in Python/Perl transitions.
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
  sanitization and promotion of captured bundles remain.

- [~] Preserve complete per-version dependency expressions in the graph. The
  compact resolver snapshot and VersionInfo now retain all five dependency
  classes per version; candidate-specific traversal is covered by regression
  tests, but complex nested choice semantics remain.
- [!] Implement EAPI-correct DEPEND/RDEPEND/BDEPEND/IDEPEND/PDEPEND behavior.
  Resolver records preserve EAPI; BDEPEND before EAPI 7 and IDEPEND before
  EAPI 8 are rejected. Retained packages keep RDEPEND/PDEPEND, never IDEPEND,
  and include DEPEND/BDEPEND only according to `--with-bdeps`. Source versus
  binary transaction roots, cross-root BROOT/SYSROOT placement and a full
  Portage differential matrix remain.
- [!] Implement complete atom semantics, slots, subslots and repository constraints.
  Repository-qualified targets and dependencies now reject candidates from the
  wrong repository, including installed candidates. Identical CPVs from
  multiple repositories now remain distinct resolver candidates, unqualified
  selection uses repository priority, and `::repo` can select a shadowed copy.
  Remaining work covers the rest of complete atom/EAPI semantics.
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
  and removal actions preserve exact version/slot/repository identity. Enforcing
  remove-before-merge ordering belongs to the P4 transaction scheduler and
  remains a live-mutation gate.
- [~] Implement virtual/provider selection and provider preference. Provider
  atoms now enforce version, slot and USE constraints and provider alternatives
  use bounded transactional rollback. EAPI virtual semantics and real-world
  preference parity still need differential coverage.
- [~] Implement any-of groups with installed and minimal-change preferences.
  Group identity, installed preference, conditional USE dependencies and
  unavailable-alternative filtering are implemented. Installed preference now
  searches every installed slot rather than only the numerically highest
  instance. Alternatives are now
  attempted transactionally with complete search-state rollback and bounded
  backtrack accounting; nested groups still need differential coverage.
- [x] Implement circular dependency detection and useful diagnostics. Active
  dependency paths emit one deterministic cycle chain without recursing to a
  depth-limit failure or rejecting an otherwise resolvable plan.
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
- [ ] Implement real backtracking with bounded decision history.
- [~] Version constraints are now accumulated per CP/slot, enforce one final
  candidate per slot, and consume the configured backtrack budget when a later
  constraint revises an earlier version choice. Any-of choices now use full
  branch snapshots. Provider alternatives now also use transactional exploration
  with installed preference; concurrent branch evaluation remains.
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
  candidates omit source-only `DEPEND`/`BDEPEND` while retaining
  `RDEPEND`/`IDEPEND`/`PDEPEND`; `--usepkgonly` now fails when no usable binary
  exists, including under `--nodeps`. GPKG metadata and remote-binhost candidate
  discovery remain under P9.
- [~] Tag every dependency edge with its PMS filesystem domain: `BDEPEND` and
  `IDEPEND` use `BROOT`, `DEPEND` uses `SYSROOT`, and runtime/post dependencies
  use `ROOT`. Separate installed-state views and cross-root solving remain.

### Tests

- [ ] Differential plan tests against `emerge -p` on a curated corpus.
- [~] Differential plans now compare source versus binary merge intent from
  Arise JSON and Portage's `[ebuild]`/`[binary]` output. Expand the corpus across
  `-k`, `-K`, `--binpkg-respect-use`, source fallback and cross-root cases.
- [ ] Capture the development laptop's current broken-world resolver state as a
  sanitized fixture, including removed ebuilds, stale Python targets, virtual
  transitions, blockers and overlays, before repairing the live installation.
- [!] Current laptop `--update @world` differential is intentionally failing:
  Arise now finds 102 actions in 10.43 s versus Portage's 135. Normalized CPV
  comparison leaves 41 one-sided entries (including four Arise-only entries).
  Do not treat successful resolution as parity until remaining USE_EXPAND
  rebuild propagation, slot-operator rebuilds and action intent match Portage.
- [ ] Golden tests for blockers, slots, subslots, virtuals and any-of groups.
- [ ] Property tests for topological ordering and solution satisfaction.
- [~] Add a mandatory post-solve whole-state verifier before permitting a real
  transaction: overlay the proposed installs/removals on the VDB snapshot and
  prove every retained and planned dependency, blocker, slot and USE constraint.
  Resolve results now carry an explicit verified/failed/skipped-nodeps/incomplete
  status, JSON plans expose it, and the production non-pretend boundary rejects
  every result not marked verified. Pretend remains available for diagnostics;
  `--nodeps` cannot silently cross into execution. The verifier's remaining
  dependency/root semantics and portable parity corpus still block completion.
  The current laptop world case produces a 138-action Arise plan while Portage
  reports Python-target same-slot conflicts; Arise must prove that its larger
  plan resolves those constraints rather than merely omitting reverse-installed
  edges.
- [~] Post-solve verifier now overlays multiple installed slots, preserves
  candidate versus installed USE/dependency metadata, and validates affected
  retained reverse constraints. Exact per-version planned removals no longer
  erase unrelated parallel slots from the overlaid state, and removals now join
  installs in the affected-name set so retained consumers are revalidated.
  Regression matrices cover a failed replacement, a complete-graph repair and a
  removal that breaks a retained runtime dependency; their verification status
  and structured conflict records are asserted. On the laptop world transition it reports 22
  Python/Rust conflicts instead of returning a false success; @system remains
  an exact conflict-free 11-package plan. Next feed these structured failures
  into complete-graph rebuild expansion and backtracking.
- [ ] Mutation tests that remove or invert constraints and must fail.
- [ ] Fuzz atom and dependency expression parsers with round-trip invariants.
- [ ] Regression test for `net-im/signal-desktop-bin` on the laptop snapshot.

Acceptance gate:

- Arise's candidate set, closure and action intent match emerge for the corpus,
  except for explicitly documented and tested improvements.

## P4 — real EAPI/ebuild execution ABI

### Overlay-owned Go dependency supply chain

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
  unprivileged user namespace. Wiring FEATURES/RESTRICT policy, cancellation
  escalation and the complete environment contract remain. The request can
  carry a typed Manifest-verified artifact set; the worker revalidates it before
  startup, injects DISTDIR, and enhanced isolation binds it read-only.
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
  source/dist/work bind layout remain release blockers.
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
  likewise reserved from environment injection. Log layout and the remaining
  PMS/package identity variables remain.
- [~] Implement a minimum complete helper ABI for current supported EAPIs.
  Explicit-status eapply/eapply_user, emake, econf, dodoc and einstalldocs now
  support the EAPI 7/8 default prepare/configure/compile/test/install path.
  Full option semantics and the remaining helper families remain blockers.
- [ ] Execute pkg_setup, source phases, pkg_preinst/postinst and pkg_prerm/postrm.
- [ ] Honor RESTRICT and PROPERTIES.
- [~] Capture structured logs, phase events, QA notices and exit status. Ordered
  phase/log/result events and one terminal status are enforced; separate stderr,
  QA producers, persistent logs and cancellation events remain.
- [x] Preserve the static Go control plane when Python is unavailable. The
  protocol and isolation launcher are Go and execute Bash directly without
  importing or invoking Portage Python.
- [x] Declare supported EAPIs and reject unsupported EAPIs before mutation.
  The initial ABI declares EAPI 7 and 8; request validation happens before
  sandbox/namespace startup, and the sourced declaration must match preflight.

### Tests

- [~] Synthetic fixtures cover discovery, exported phases and EAPI 8 default
  prepare/configure/compile/test/install; EAPI 7 and lifecycle/default-unpack
  matrices remain.
- [~] Eclass inheritance and exported phase fixtures cover nested inheritance,
  missing eclasses, cycles and exported discovery/execution. Repository-master
  precedence and representative real Gentoo eclasses remain.
- [~] Helper ABI contracts cover patch precedence/failure, Makefile defaults,
  image installation and default docs for the initial helper subset.
- [ ] Environment snapshot comparisons with Portage.
- [ ] Build representative packages: trivial, autotools, cmake, meson, Go,
  Python, Rust, kernel module, binary-only and config-protected.
- [ ] Failure injection at every phase.

Acceptance gate:

- Representative packages produce equivalent image trees and lifecycle effects
  under Portage and Arise.

## P5 — fetch and verification

- [~] Reuse DISTDIR atomically and avoid duplicate concurrent downloads. A
  shared Fetcher coordinates identical Manifest identities per DISTDIR, reuses
  valid files and commits unique temporary files by atomic rename. Linux
  processes additionally coordinate through symlink-safe, context-cancellable
  per-artifact flock files; subprocess fixtures prove contention and release.
  Resumable partial state remains. Production source fetch-only and legacy
  rebuild preparation call the same AcquireManifest entry point.
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
- [ ] Implement RESTRICT=fetch and pkg_nofetch behavior.
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

Acceptance gate:

- Fetch-only and normal builds never consume unverified distfiles when a
  Manifest digest is available.

## P6 — transactional merge and unmerge

- [!] Design and implement an operation journal.
- [~] Run collision/ownership validation before live-root mutation. A VDB
  ownership scanner and image collision fixtures exist, but the prototype merge
  path does not invoke them and remains unreachable from production commands.
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
- [ ] CONFIG_PROTECT behavioral matrix, including differential fixtures against
  Portage merge output and subsequent dispatch-conf/etc-update handling in a
  disposable ROOT.
- [~] File ownership and cross-package collision fixtures cover the standalone
  scanner; transaction-integrated collision-protect/protect-owned cases remain.
- [ ] Kill process at every journal boundary and recover.
- [ ] Concurrent non-conflicting and conflicting merge tests.

Acceptance gate:

- An interrupted operation is either fully committed or recoverable, and
  Portage can read and manage the resulting VDB state.

## P7 — dependency-aware concurrent scheduler

- [ ] Replace FIFO worker distribution with a DAG ready queue.
- [ ] Release dependents only after successful dependency completion.
- [ ] Propagate failures and implement keep-going by independent subgraph.
- [~] Apply load-average backpressure in production workers. Linux loadavg
  sampling and the legacy rebuild context exist; the future DAG resource
  scheduler does not consume them yet.
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
- [~] Correctly implement pretend, ask, fetchonly and buildpkgonly. Source
  fetch-only now executes the resolved plan through the shared Manifest-backed
  verified DISTDIR pipeline without entering build or merge; binary fetch,
  complete ask semantics and buildpkgonly remain.
- [ ] Update world only for successful explicit installs and respect oneshot.
- [~] Mark resume nodes complete after transaction commit. Versioned resume
  persistence, load, skip-first and completion primitives have fixtures, but
  no P6 transaction commit exists to authorize completion.
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
- [x] Verify pretend performs zero mutations. A copied-binary live check matched
  the Signal plan while before/after VDB and world fingerprints were identical.

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
- [ ] Absorb `quickpkg`'s role: create a metadata-complete binary package from
  an installed VDB instance, with explicit handling for preserved libraries,
  config files, hardlinks, xattrs and build IDs.
- [ ] Design binary production as a reusable API for future tinderbox and
  stage4 automation, including deterministic output, provenance, signing,
  atomic repository publication and concurrent multi-package builds.
- [ ] Retain XPAK compatibility only where useful and tested.

### Tests

- [ ] Cross-read packages produced by Portage and Arise.
- [ ] Cross-install into isolated roots.
- [ ] Remote binhost fixture with Packages index updates.
- [ ] Round-trip installed package -> quickpkg-equivalent GPKG -> isolated ROOT
  and compare files, metadata and VDB state.
- [ ] Reproducibility tests for repeated tinderbox builds and interrupted
  publication recovery.
- [ ] Corruption, signature and incompatible-USE tests.

Acceptance gate:

- Portage and Arise can consume each other's supported binary packages.

## P10 — remaining emerge and maintenance behavior

- [ ] Extract stable, independently versioned Go libraries from Arise's
  Portage-compatible core so other tooling can reuse atoms and dependency
  expressions, repository/VDB metadata, profile/config evaluation, immutable
  snapshots, Manifest/fetch verification, plan/conflict records and the phase
  protocol without importing the Arise CLI or mutable implementation details.
  Define compatibility policy, narrow interfaces, conformance fixtures and
  examples; keep orchestration, product policy and live mutation in Arise.
- [ ] Publish a composable Bash runtime for ebuilds, eclasses and Gentoo tooling
  that replaces `set -e`/`set -euo pipefail` folklore with explicit checked-call,
  pipeline, cleanup/defer, typed diagnostic, stack-context and status-propagation
  helpers. Preserve expected nonzero statuses in conditionals and probes, never
  let a caller's shell options silently change library semantics, and make
  failures round-trip through Arise's structured phase protocol. Validate the
  library across functions, subshells, command substitutions, pipelines,
  traps/signals and nested sourcing on every supported Bash version.

- [ ] Implement accurate `emerge --info`-equivalent output.
- [ ] Implement package sets and list-sets behavior.
- [ ] Complete autounmask suggestions and atomic config writes.
- [ ] Implement pkg_config execution.
- [ ] Complete dispatch-conf-style recursive config management: protected-tree
  discovery; stable candidate ordering; update/keep/skip/edit/merge/diff/quit
  decisions; identical-file and safe auto-merge handling; isolated archive and
  rollback; configured pre/post-session and pre/post-update hooks; metadata
  preservation; explicit interruption recovery; and differential tests against
  Portage's dispatch-conf and etc-update in a disposable ROOT. Cover
  etc-update's preen and automatic modes, explicit scan paths, and
  PORTAGE_CONFIGROOT/EROOT behavior. Run dispatch-conf inside a chroot or
  mount-isolated root because it has no root-selection CLI. Never use the live
  host configuration as a behavioral fixture.
- [ ] Wire preserved-rebuild and revdep-rebuild through the safe planner/executor.
- [ ] Supplant `perl-cleaner`: detect Perl subslot/ABI transitions, stale module
  files and packages linked to obsolete libperl instances; produce one
  resolver-backed pretend/rebuild plan with explicit reasons.
- [ ] Add a Python repair/cleaner workflow: detect stale interpreter targets,
  invalid shebangs, orphaned site-packages, extension modules linked against
  removed libpython ABIs and packages whose installed PYTHON_TARGETS no longer
  satisfy current profile policy.
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
- [ ] Deterministic concurrent logs that remain readable.
- [ ] Content-addressed fetch/build cache with verified reuse.
- [ ] Safe operation preview including exact filesystem/config/VDB mutations.
- [ ] First-class rollback and crash recovery.
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
- [ ] Reproducible privacy-reviewed support bundle containing configuration and
  repository fingerprints, normalized plan/conflicts, resolver trace and
  relevant VDB metadata; redact paths, hostnames, mirrors and secrets by
  default and make the bundle diffable across machines.
- [ ] Transaction rehearsal in a disposable ROOT or overlay snapshot, including
  lifecycle probes and post-state verification, before offering the identical
  journaled plan for approval on the live root.
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
