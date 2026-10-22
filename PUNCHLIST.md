# Open work

Updated 2026-10-22. This is the remaining backlog, not release history.
Remove completed items; Git retains them. Each item requires observable
behavior and failure-path evidence before closing.

0.0.43 adds profile inspection and comparison in its [release notes](docs/releases/0.0.43.md).
The full fresh-stage3 run is explicitly deferred for this release.

## Next fixes and validation

- [ ] **N10 — Profile explanation queries and migration planning.** Expose Gentooling's
  assignment trace through targeted CLI queries and connect profile comparisons
  to dependency resolution and hashed plans.
  **Done when:** [explanations](docs/profile-inspection.md) identify inherited changes and overrides, and
  candidate-profile plans use isolated configuration with reproducible inputs.
  Advertised architecture matches alone do not establish migration safety.

- [ ] **S01 — Expand frozen Portage comparisons.** Add slot rebuild, uninstall,
  damaged-world and configuration-diversity cases to the existing matrix.
  **Done when:** every difference is explained or reduced to a regression,
  using [the comparator](internal/plancompare) and identical frozen inputs.

- [ ] **S02 — Complete option and resume semantics.** Persist original build,
  binary, root and job options; cover skipfirst, nodeps/onlydeps, ask/pretend/JSON,
  jobs/load/cancellation and alternate roots.
  **Done when:** [CLI tests](cmd/arise/install_planning_audit_test.go) prove
  progress, world, exit status and side effects, including hard interruption.

- [ ] **S03 — Broaden transaction failures.** Extend real source/binary workers
  through config collisions, modified-file retention, unmerge and interruption.
  **Done when:** [executor](internal/executor) and [merge](internal/merge) tests
  verify payload, CONTENTS, VDB, world and journal state at each failure boundary.

- [ ] **S04 — Complete recovery-set restore coverage.** Restore multiple
  dependent packages with local/configuration changes and interrupted pruning.
  **Done when:** [recovery tests](internal/recoveryset) prove dependency/linkage
  correctness, idempotent retry and retention of every required recovery object.

- [ ] **S06 — Audit supported claims and diagnostics.** Cover ambiguous targets,
  retained packages, historical build dependencies and pre-existing failures.
  **Done when:** the [compatibility matrix](docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
  matches command-level evidence and unsupported paths fail before mutation.

- [ ] **S07 — Establish performance budgets.** Measure equivalent cold, warm
  and incremental workloads, including memory, CPU, I/O and tail latency.
  **Done when:** [benchmarks](BENCHMARK_MATRIX.md) retain machine-readable
  baselines, noise bands and explicit regression budgets.

## Compatibility and operations

- [ ] **N01 — Metadata and policy diversity.** Cover uncached overlays,
  repository/profile precedence, USE/keywords/licenses, environment and
  ROOT/SYSROOT/BROOT separation.
  **Done when:** a [configuration corpus](internal/portage) matches Portage and
  incomplete metadata cannot authorize execution.

- [ ] **N02 — Damaged-world repair.** Compute complete ordered repair closures,
  including alternate providers and retained slots.
  **Done when:** [resolver fixtures](internal/resolve) independently validate
  repairs or explain the full conflict without trial-and-error subsets.

- [ ] **N03 — Phase, fetch and binary lifecycle parity.** Implement required
  binary hooks and expand EAPI/environment, fetch integrity and cancellation cases.
  **Done when:** the [package corpus](misc/PACKAGE_FIXTURE_CORPUS.md) matches
  declared reference behavior, including lifecycle failures and source fallback.

- [ ] **N06 — Binary interoperability and trust.** Expand binary dependency
  authority, multiple instances, signatures, corruption, USE/ABI mismatch and
  concurrent index publication/retention.
  **Done when:** [binary tests](internal/binpkg) prove cross-reading with Portage
  and reject invalid inputs without replacing valid state.

- [ ] **N07 — Maintenance workflows.** Finish custom sets, depclean/prune,
  autounmask writes, info, dispatch-conf, rebuilds and Python/Perl cleaners.
  **Done when:** each [workflow](docs/tool-equivalents.md) has reference,
  pretend, confirmation and interruption coverage before promotion.

- [ ] **N08 — Reduce measured repeated work.** Address indexing, graph memory,
  incremental sync and query costs only after profiling.
  **Done when:** [equivalent benchmarks](docs/performance-results.md) improve
  without moving cost into an unmeasured stage or weakening complexity limits.

- [ ] **N09 — Operational output.** Prioritize active recovery, explain estimate
  uncertainty, centralize color and keep parallel progress readable.
  **Done when:** [presentation contracts](docs/planning/README.md) cover
  redirected, no-color, empty, busy, error and recovery states.

## Deferred

- [ ] **S05 — Fresh-stage3 acceptance harness.** Use a disposable VM with normal
  UID/GID ownership and pinned repositories/caches; run update, interruption,
  resume, linkage and reboot checks.
  **Done when:** the [runbook](docs/fresh-stage3.md) reproduces a complete run
  with retained inputs and before/after state. Current single-UID fixtures do
  not satisfy this gate. No new stage3 run is planned for 0.0.43.

- [ ] **N04 — Filesystem snapshot rollback.** Certify Btrfs, OpenZFS and LVM
  separately, including capacity, topology, partial failure and reboot recovery.
  **Done when:** each [provider](docs/planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md)
  proves recovery on a disposable system; package journals remain distinct.

- [ ] **N05 — System construction.** Progress from system/world updates to
  empty-tree and stage1 construction only after S03–S05.
  **Done when:** [G1–G4](docs/fresh-stage3.md) retain repeatable evidence,
  boot-critical ordering and static recovery tools.

- [ ] **L01 — Further library extraction.** Extract only for a concrete consumer.
  **Done when:** [public APIs](docs/library-roadmap.md) pass independent consumer
  contracts without CLI state; generic solver code stays separate from Gentoo policy.

- [ ] **L02 — Remote builds and images.** Start after S03–S05 and N06.
  **Done when:** [versioned artifacts](internal/binpkg) allow verified,
  reproducible construction and interrupted remote operations can resume.

- [ ] **L03 — Planning assistance.** Start after S01/S02/S06.
  **Done when:** [diagnostics](docs/planning/DIAGNOSTIC_INTELLIGENCE_PLAN.md)
  provide inspectable provenance, pretend output and stale/conflicting-input tests.

- [ ] **L04 — musl certification.** Start after the glibc stabilization gates.
  **Done when:** a musl lane passes the same platform, phase, ELF,
  [preserved-library](internal/preserved) and recovery contracts.

## Closing work

Record source revision, commands, skips and limits in a scoped review. Run
required tests, vet, formatting and relevant race/schema/atomicity checks.
Keep synthetic, real-worker, reference and fresh-system evidence distinct.
Releases require offline artifact and ebuild validation plus published digest
verification; a deferred acceptance gate must remain explicitly unrun.
