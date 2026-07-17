# P0-P4 remaining-task breakdown

This is the executable breakdown behind the punch-list status as of
2026-07-17. A task is not complete until its listed validation is automated.

## Recommended critical path

1. Freeze portable resolver and benchmark snapshots (P0/P3).
2. Close the remaining configuration boundaries (P2).
3. Make the post-solve verifier mandatory and close resolver semantics (P3).
4. Complete the execution protocol, environment, phase discovery, and policy
   rejection layers (P0B/P4).
5. Implement the helper/phase ABI and representative build corpus (P4).
6. Package the Go dependency graph for fully offline recovery builds (P4).

Real-root mutation stays disabled throughout P0-P4. P6 owns journaling and
live merge safety.

## P0A - performance laboratory

### P0A.1 Portable measurement metadata

- Capture CPU, memory, storage, filesystem, kernel, Go, Python, Portage, eix,
  repository/profile/VDB fingerprints, and Arise revision in every result.
- Reject comparisons whose state fingerprints differ.
- Validation: schema round-trip plus a fixture with an intentional fingerprint
  mismatch that must fail.

### P0A.2 Workload and cache-state matrix

- Finalize small, medium, and full-world immutable workloads.
- Run cold-cache, warm-cache, and one-change incremental variants separately.
- Collect wall time, CPU time, peak RSS, bytes read/written, process count,
  median, and p95.
- Validation: repeated reports on the same snapshot contain every metric and
  classify cache state unambiguously.

### P0A.3 Regression policy and CI gates

- Set per-operation noise bands and regression budgets from repeated laptop
  measurements rather than one global percentage.
- Gate end-to-end workloads; retain microbenchmarks as diagnostics.
- Make intentional result mismatches and performance regressions fail CI.
- Define risk-weighted test-coverage floors for deterministic core, network
  integration, and benchmark harness lanes.
- Validation: seeded correctness, latency, allocation, and coverage regressions
  must each trip the expected gate.

## P0B - unsupported behavior safety

### P0B.1 Phase and FEATURES rejection matrix

- Replace the synthetic `src_prepare` implementation with the real P4 ABI.
- Inventory every parsed FEATURES value and classify it as implemented,
  explicitly ignored by Portage semantics, or rejected with a typed error.
- Apply the same rule to every lifecycle and source phase.
- Validation: table tests prove no unsupported phase or feature returns success.

### P0B.2 Mutation-lock handoff

- When P6 transaction entry points exist, acquire the Portage-compatible VDB
  lock before the first mutable step and hold it through commit/recovery state.
- Validation: subprocess contention, interruption, and stale-owner recovery.

## P1 - package-state model

The listed P1 implementation and fixtures are complete. Remaining work is
milestone hardening rather than a known model feature gap:

- Package the repository/VDB equality corpus as a portable same-snapshot gate.
- Run the index transaction and deterministic-ingestion suites under the race
  detector and interruption stress.
- Validation: exact available/installed CPV equality and readable previous
  snapshot after every injected index interruption.

## P2 - configuration and profile evaluation

### P2.1 Execute package.env results

- [x] Pass the composed per-package environment into the P4 package-policy
  request builder.
- [x] Preserve incremental USE/FEATURES semantics and reject Bash startup
  injection plus protocol-owned directory/control variables before startup.
- [x] Distinguish effective global configuration, package.env, retained one-shot
  command environment and explicit request layers in the final normalized
  worker environment. Pre-command values are retained so incremental command
  tokens are applied once at the correct final precedence.
- [x] Validate overlapping package.env and command/request scalar plus
  incremental rules in portable tests. Production action invocation of this
  completed handoff is tracked under P4 rather than keeping P2 artificially open.

### P2.2 Privilege-boundary command environment — complete

- [x] Differential-test direct, root, filtered, and explicitly supplied
  command environments for policy, root/repository selectors, build controls,
  and output controls. Direct allowlist/precedence tests and root captures pass;
  the immutable evidence manifest records sanitized hashes and fingerprints.
- [x] Document which variables must be preserved by the caller rather than
  trying to recover variables removed by sudo.
- Validation: copied-binary tests match emerge without persisting one-shot
  settings.

### P2.3 Portable parity corpus — complete

- [x] Freeze the current 100-CPV effective-USE, 100-package visibility, 25-mask,
  shallow `@system`, and Signal cases into sanitized fixtures.
- [x] Remove the stale punch-list statement that package effective USE remains a
  future target once the portable fixture replaces the live-only assertion.
- Validation: portable policy/precedence fixtures remain executable, while the
  sanitized evidence manifest freezes exact live results and the repository,
  profile, Portage and Arise identities independently of the next sync.

## P3 - dependency and resolver correctness

### P3.1 Snapshot the difficult states first

- Sanitize and freeze the current broken-world state: stale Python/Perl
  targets, removed ebuilds, overlays, virtual transitions, blockers, and slot
  operators.
- Add a separate conflict-free world snapshot so successful closure parity is
  measurable independently from conflict rendering.
- Validation: fixtures reproduce the current normalized differences without
  reading live `/var/db/repos`, `/etc/portage`, or VDB.

### P3.2 Make whole-state verification a transaction gate

- [x] Overlay all planned installs/removals on every installed slot. Planned
  blocker removals now retain exact version/slot/repository identity, qualified
  blockers preserve unrelated slots, and unqualified blockers enumerate all
  matching installed instances. Both installs and removals trigger retained
  reverse-dependency verification; failed replacement, repaired replacement,
  general uninstall and dependency-breaking removal matrices assert the final
  status. Explicit removal verification is strict: an installed-only consumer
  cannot downgrade breakage to depclean advice. Immutable ROOT, SYSROOT and
  BROOT views now gate all five dependency classes, providers, any-of groups and
  blockers without aliasing a ROOT removal into another domain. Planned action
  identity, blocker removal, dependency ordering and JSON output now retain the
  destination domain; identical package versions can coexist as independent
  ROOT, SYSROOT and BROOT actions. Native aliased roots collapse back to one
  ROOT action, preserving ordinary Gentoo plans.
- [x] Carry an explicit verification status on every resolve result and JSON
  plan, and reject non-pretend execution unless the status is `verified`.
  Early-returned, partial, errored and `--nodeps` plans remain inspectable but
  are fail-closed at the execution boundary.
- Validate retained and planned dependencies, blockers, slots/subslots, USE,
  and filesystem domains.
- Feed verifier failures back into complete-graph expansion and backtracking.
- Refuse executable plans unless verification succeeds.
- Validation: mutation tests that remove or invert each constraint must fail;
  exact shallow `@system` and Signal plans must pass.

### P3.3 Finish EAPI dependency/root semantics

- Complete source/binary DEPEND, RDEPEND, BDEPEND, IDEPEND, and PDEPEND behavior.
- Solve against distinct ROOT, SYSROOT, and BROOT installed-state views.
- Cover source roots, binary roots, cross-root placement, and `--with-bdeps`.
- Validation: full EAPI/source/binary/root differential matrix against emerge.

### P3.4 Complete atom and expression semantics

- Inventory remaining PMS atom/EAPI forms instead of treating the current
  release-blocker as an open-ended item.
- Finish nested any-of choice behavior and virtual/provider preference.
- Add golden blockers, slots, subslots, virtuals, and any-of fixtures plus atom
  and dependency-parser fuzz round trips.
- Validation: curated corpus has identical candidates, closure, action intent,
  and deterministic ordering.

### P3.5 Backtracking and conflict output

- Replace remaining branch snapshots with bounded decision history and undo-log
  exploration for version, any-of, and provider decisions.
- Feed complete-graph and reverse-VDB failures into the same search.
- [~] Finish structured installed-versus-replacement and autounmask causes, then
  render human output from those structures. Slot conflicts and post-solve
  verification failures now have typed records; candidate-state completeness,
  autounmask causes and structure-driven human rendering remain.
- Validation: backtrack limits are honored; repeated runs yield byte-identical
  actions/conflicts; forced scaling remains within the P0 budget.

### P3.6 Close live plan differences

- Fix interpreter-transition candidate/reinstall classification.
- Finish reverse-VDB subslot rebuild propagation and action intent.
- Expand source/binary comparisons across `-k`, `-K`, USE compatibility,
  fallback, and cross-root cases.
- Validation: exact conflict-free `@world --complete-graph` parity and fully
  explained equality/differences on the intentionally broken snapshot.

### P3.7 Solver assurance

- Add topological-order and solution-satisfaction property tests.
- Add constraint mutation tests and the permanent Signal snapshot regression.
- Run parser fuzzing and resolver race/stress suites.

## P4 - real EAPI/ebuild execution ABI

### P4.1 Complete protocol control behavior

- Add cancellation escalation and worker/process-tree cleanup.
- Define the complete environment allowlist and typed diagnostic/event schema.
- Capture stdout and stderr as ordered structured logs and support QA events.
- Validation: high-job-count cross-talk, cancellation, malformed-event, missing
  result, duplicate result, and process-leak tests.

### P4.2 Finish Portage-compatible isolation policy

- Map FEATURES, RESTRICT, PROPERTIES, phase, privilege, and platform state to
  independent sandbox, userpriv/userfetch, network, IPC, mount, and PID choices.
- Keep `sandbox` as the default filesystem mediator. Direct namespaces must
  capability-degrade with warnings and must not require user namespaces.
- Keep Bubblewrap explicit and strict; add its eventual build/runtime feature
  gate without changing package results.
- [~] Define controlled read/write binds for repository/eclasses, DISTDIR,
  WORKDIR, image, temp, logs, ROOT, SYSROOT, and BROOT. Ebuild/eclass/DISTDIR,
  work/source/image, independent read-only root domains, and writable temp/home
  mappings exist; persistent log layout and phase-specific root permissions
  remain.
- Validation: policy matrix plus host-read/write escape fixtures for both
  backends where available.

### P4.3 Phase discovery and supported EAPIs

- Declare the initial supported EAPI set and reject all others before work.
- Discover exported phases and implement EAPI default phases.
- Source inherited eclasses in deterministic repository-master order.
- Validation: one synthetic ebuild per phase/EAPI plus inherited/exported phase
  fixtures and unsupported-EAPI preflight failures.

### P4.4 Environment and directory contract

- [~] Create the Portage-compatible build directory layout and required
  variables. WORKDIR/S/D/ED, ROOT/SYSROOT/BROOT, T/TMPDIR/TMP/TEMP and HOME are
  controlled by the request; log and remaining PMS identity variables remain.
- [~] Integrate P2 package.env results and ROOT/SYSROOT/BROOT domains. The
  package-policy builder composes package.env and copies independent root/scratch
  domains while rejecting overrides; production action construction and Portage
  snapshot parity remain.
- Ensure the Go control plane remains usable without Python.
- Validation: normalized environment/directory snapshots against Portage.

### P4.5 Helper and lifecycle ABI

- Implement the minimum complete helper ABI for each declared EAPI.
- Execute pkg_setup, source phases, pkg_preinst/postinst, and pkg_prerm/postrm
  with correct failure boundaries.
- Honor RESTRICT and PROPERTIES throughout execution.
- Validation: helper contract tests and failure injection at every phase.

### P4.6 Representative image-tree corpus

- Build trivial, autotools, CMake, Meson, Go, Python, Rust, kernel-module,
  binary-only, and config-protected fixtures.
- Compare normalized image trees, metadata, logs, and permitted lifecycle
  effects with Portage.

### P4.7 Offline Go dependency supply chain

- Generate overlay module packages and the Arise dependency set from
  `go.mod`/`go.sum`, pinned by version and source digest.
- Record license, upstream, transitive relationships, and security ownership.
- Populate an isolated module source/cache and build the Arise ebuild without a
  proxy, network, vendor tree, or mutable global Go cache.
- Compare this scheme with the locked release archive and retain a tested
  offline fallback during migration.
- Validation: CI detects module/overlay divergence and an empty-cache,
  no-network install succeeds using overlay-managed sources only.

### P4.8 Broken-Python recovery gate

- Preserve a sanitized snapshot of the current machine state in which Portage
  and Gentoolkit Python entry points are unreliable while the static Arise
  control plane remains usable.
- Resolve, verify, fetch and build the interpreter/control-plane repair closure
  without invoking Portage Python or weakening dependency checks with an
  implicit nodeps mode.
- Rehearse the journaled repair in a disposable ROOT before any live mutation;
  retain explicit administrator-authorized oneshot/nodeps only as an emergency
  escape hatch with prominent unsatisfied-dependency reporting.
- Validation: Arise produces and executes a verified recoverable repair plan in
  the disposable broken-root fixture where emerge cannot complete the plan.

## Definition of P0-P4 complete

- All P0-P4 punch-list entries and their acceptance gates are automated.
- Portable snapshots replace claims dependent on the current live laptop.
- Every executable plan passes whole-state verification.
- Representative ebuilds produce Portage-equivalent image trees and lifecycle
  effects, while real-root mutation remains gated on P6 journaling.
- Correctness and performance reports refer to the same immutable inputs.
