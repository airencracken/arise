# Arise punchlist

Updated 2026-09-08. Only unfinished work belongs here. Remove an item when its
acceptance condition is met; Git history and linked reviews retain completed
work. A passing unit test does not establish full Portage parity.

Finish stabilization before expanding scope. Batch related fixes and their
regressions; release on an explicit decision, not after each individual fix.

## Now: stabilization

- [ ] **S01 — Compare current plans with Portage.** Capture single-package,
  deep/newuse, slot-rebuild, uninstall, build-only, and binary-only cases on the
  same repository/profile/VDB snapshot. Classify every difference, turn defects
  into portable regressions, and rerun the matrix after fixes.
  **Done when:** every case has a reproducible equivalence verdict or a clearly
  documented unsupported boundary, with no unexplained difference.
  Start with [reference fixtures](misc/REFERENCE_FIXTURES.md) and
  [plan comparison](internal/plancompare).

- [ ] **S02 — Test interacting command options.** Cover ask/pretend/JSON,
  resume/skipfirst, nodeps/onlydeps, buildpkgonly/deep/usepkg, jobs/load/cancellation,
  and alternate configuration roots. Include exit status, output, world,
  resume, logs, and filesystem side effects.
  **Done when:** each combination has a command-level test proving its behavior
  or an actionable rejection before mutation; the compatibility matrix reflects
  that exact scope. Start with [CLI tests](cmd/arise/main_test.go).

- [ ] **S03 — Expand the disposable-root transaction matrix.** Exercise real
  phase workers through install, upgrade, config collision, modified-file
  retention, unmerge, interruption, and retry for source and binary packages.
  **Done when:** pre/post VDB, world, CONTENTS, journals, payload hashes, and
  dependency checks prove the expected committed or recovered state at every
  injected failure boundary. Start with [executor tests](internal/executor)
  and [transaction tests](internal/merge).

- [ ] **S04 — Prove complete recovery-set restore.** Restore multiple packages
  in dependency-safe order, inject failure at publication and restore boundaries,
  and retry interrupted operations. Include configuration and local changes.
  **Done when:** the restored disposable root passes dependency, linkage, and
  payload checks; retry is idempotent; pruning cannot remove required recovery
  objects. Start with [recovery-set tests](internal/recoveryset).

- [ ] **S05 — Run the fresh-stage3 acceptance gate.** Perform an Arise-only
  deep update in a disposable stage3, interrupt and resume it, then verify final
  package state and linkage.
  **Done when:** the documented procedure reproduces from a clean image and
  retains all input, plan, execution, and verification evidence. A successful
  update of an existing development host does not substitute for this gate.
  Start with [fresh-stage3 instructions](docs/fresh-stage3.md).

- [ ] **S06 — Audit diagnostics and compatibility claims.** Check complete
  command transcripts for requested versus retained packages, historical build
  dependencies, pre-existing failures, build output versus installed state,
  ambiguous targets, and actionable errors.
  **Done when:** every supported claim links to behavior-level coverage, and
  partial or unsupported workflows are described accurately in help and docs.
  Start with the [compatibility matrix](docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md).

- [ ] **S07 — Refresh performance and memory budgets.** Freeze equivalent
  cold, warm, and incremental workloads. Measure latency, CPU, allocations,
  private memory, and I/O; define noise bands before optimizing.
  **Done when:** machine-readable baselines and explicit regression budgets
  exist, and changes preserve result equivalence and the complexity ratchets.
  Start with [performance results](docs/performance-results.md).

## Next: remaining daily-driver gaps

- [ ] **N01 — Close metadata and configuration gaps.** Evaluate uncached
  overlay metadata authoritatively and test repository/profile precedence,
  USE/keywords/licenses, environment, and ROOT/SYSROOT/BROOT separation.
  **Done when:** a configuration-diversity corpus matches Portage and incomplete
  metadata cannot silently authorize a package. See [configuration code](internal/portage).

- [ ] **N02 — Complete damaged-world repair planning.** Derive the full repair
  closure and explain every rebuild/removal, without requiring trial-and-error
  oneshot/nodeps subsets. Include alternate providers and retained slots.
  **Done when:** portable damaged-state cases produce ordered, independently
  validated repairs or complete conflict explanations. Depends on S01.
  See [resolver tests](internal/resolve).

- [ ] **N03 — Broaden phase and fetch compatibility.** Expand the real-package
  corpus for EAPI helpers, environment isolation, lifecycle failures,
  RESTRICT/mirrors, integrity, and cancellation.
  **Done when:** normalized phase environments, images, and fetch outcomes match
  reference behavior for the declared supported corpus. See [phase protocol](internal/phaseproto)
  and [package corpus](misc/PACKAGE_FIXTURE_CORPUS.md).

- [ ] **N04 — Certify whole-operation snapshot rollback.** Implement and test
  Btrfs, OpenZFS, and LVM providers independently, including topology, capacity,
  failed creation/rollback, reboot, and disabled-provider behavior.
  **Done when:** each advertised backend proves recovery on a disposable system;
  package journals and overlay staging are not claimed as snapshot rollback.
  Depends on S03/S04. See the [snapshot plan](docs/planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md).

- [ ] **N05 — Promote system construction incrementally.** Complete preserved
  rebuild and system/world update gates, then empty-tree and stage1-to-stage3
  construction. Prove boot-critical ordering and preserve static recovery tools.
  **Done when:** G1–G4 each have repeatable clean-image evidence before the next
  gate is attempted. Depends on S03/S04/S05. See [fresh-stage3 instructions](docs/fresh-stage3.md).

- [ ] **N06 — Expand binary interoperability and trust tests.** Cover remote
  index refresh, multiple instances, signatures, corrupt archives, incompatible
  USE/ABI, installed-state capture, and concurrent publication/retention.
  **Done when:** valid artifacts cross-read with Portage; invalid inputs cannot
  mutate package state or replace valid published metadata. See [binary tests](internal/binpkg).

- [ ] **N07 — Finish maintenance workflows.** Complete custom sets/list-sets,
  depclean/prune, autounmask writes, accurate info, dispatch-conf parity,
  preserved/revdep rebuild execution, and Python/Perl cleaner differentials.
  **Done when:** each command has a reference comparison, pretend behavior,
  appropriate confirmation, and interruption/recovery coverage before promotion.
  Split individual workflows into scoped tasks when starting them.
  See [tool equivalents](docs/tool-equivalents.md).

- [ ] **N08 — Reduce repeated indexing and graph work.** Address eager
  repository loading, retained graph memory, incremental sync, and measured
  query-index bottlenecks.
  **Done when:** each accepted change improves an S07 workload without changing
  normalized results or moving cost into an unmeasured phase. Do not add caches
  or postings solely because they seem likely to help.
  See [performance results](docs/performance-results.md).

- [ ] **N09 — Finish operational presentation controls.** Make recovery status
  prioritize active work, explain historical build estimates and uncertainty,
  support semantic color configuration, and keep parallel progress readable.
  **Done when:** command transcripts cover empty/busy/error/recovery states,
  redirected output, no-color operation, and configuration overrides without
  changing plan semantics. See [topic plans](docs/planning/README.md).

## Later: explicitly deferred

- [ ] **L01 — Extract reusable libraries and tooling.** Generic solver,
  headless maintenance APIs, composable ebuild Bash runtime, overlay-managed Go
  modules, and ecosystem packaging.
  **Start when:** stabilization contracts are stable and a concrete consumer
  justifies each extraction. **Done when:** independent consumers pass contract
  tests without importing CLI state. See the [library roadmap](docs/library-roadmap.md).

- [ ] **L02 — Build remote production and image workflows.** Remote builder,
  tinderbox state bundles, reproducible stage4/cloud images, and verified shared
  artifact caches.
  **Start when:** S03–S05 and N06 are satisfied. **Done when:** interrupted remote
  operations reproduce or resume from public versioned artifacts, with verified
  inputs, outputs, and authority boundaries. See [binary-package code](internal/binpkg).

- [ ] **L03 — Add planning and maintenance assistance.** Counterfactual
  configuration plans, upgrade-risk reports, migration advice, salvage plans,
  and a maintenance UI.
  **Start when:** S01/S02/S06 provide reliable explanations and execution
  contracts. **Done when:** every proposed action has inspectable provenance,
  pretend output, and stale/conflicting-input tests. See [diagnostics](internal/diagnostic).

- [ ] **L04 — Certify musl support.** Add platform identity, resolver/config
  fixtures, ELF/preserved-library tests, phase ABI coverage, and static recovery
  execution on musl.
  **Start when:** the glibc stabilization gates pass. **Done when:** a repeatable
  musl integration lane passes the same declared package/recovery contracts.
  See [ELF and preserved libraries](internal/preserved).

## Rules for closing work

Record the exact source commit, commands, environment limitations, and evidence
in a review under [docs/reviews](docs/reviews). Then remove the finished item
from this file. Keep remaining scope specific; do not replace it with a story
about what has already shipped.

For code changes: formatting and diff checks, full tests, vet, appropriate race
coverage, schema/API/CLI contracts, and defect-specific regressions must pass.
Use property/adversarial tests and targeted source mutations for important
invariants. Parser changes need fuzz smoke evidence; durable writes need
atomicity and interruption tests. Preserve the existing complexity limits.

Report synthetic, real-worker, same-snapshot Portage, and fresh-stage3 evidence
separately. Never claim an unrun gate passed. A requested release additionally
needs reproducible offline artifacts, source/binary ebuild validation, and
verification of published digests and overlay versions.

Keep Gentoo state authoritative, internal caches rebuildable, inputs immutable,
results deterministic, diagnostics bounded, and mutations journaled or atomic.
Unsupported behavior must fail explicitly. Performance improvements must retain
correctness and interoperability with ordinary Gentoo tools and public formats.
