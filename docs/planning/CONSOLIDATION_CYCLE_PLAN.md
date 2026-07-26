# Arise consolidation cycle plan

## Purpose

The next cycle should make Arise easier to install, measure, and administer
before another long system-construction proof. It deliberately balances four
workstreams:

1. a useful and maintainable Gentoo overlay;
2. a bounded correctness-gated performance pass;
3. the first production-quality maintenance workflow;
4. preparation and execution of the fresh-stage3 G1 gate.

This is an orchestration plan beneath `../../PUNCHLIST.md`. It does not replace
the detailed performance, execution, recovery, or construction gates.

## Entry condition

Begin after the current 19-action live world update has:

- committed every package transaction;
- passed journal, resume, preserved-library, VDB, log, and plan-parity checks;
- survived the required reboot/runtime probes;
- produced a dated checkpoint with exact binary and plan identities.

If verification exposes another correctness defect, reduce it to a regression
and repair the affected state before starting performance claims or G1.

## Scope and order

Overlay work and baseline measurement may overlap, but changes remain small and
reviewable. The cycle order is:

1. overlay foundation and installable Arise package;
2. frozen performance baseline and one bounded optimization;
3. `arise maintain world --check/--fix`;
4. fresh-stage3 G1 rehearsal and acceptance run.

The cycle ends after G1 evidence is published or a failed G1 run has produced a
portable regression and an explicit continuation decision. It must not expand
into empty-tree G2, a full maintenance-command suite, or an unlimited solver
rewrite.

## C1 — useful overlay

### Repository shape

Use the existing `airencracken/arise-overlay` repository as the maintained
Gentoo repository. Keep its source revision, dependency archive, and release
metadata mechanically bound to this repository without duplicating an overlay
subtree here. It must contain ordinary Gentoo repository metadata, the Arise
package, maintainer metadata, and profiles suitable for `eselect repository`
or a standard `repos.conf` entry.

Provide:

- a versioned release ebuild built from an immutable release artifact;
- a live `9999` ebuild clearly excluded from release/reproducibility claims;
- installation of the binary, man page, Info documentation, Bash completion,
  and only the support assets intended for users;
- explicit runtime/build dependencies and supported Go version constraints;
- a short bootstrap, upgrade, uninstall, and troubleshooting guide;
- a package metadata owner and documented release-update procedure.

### Offline dependency policy

The first useful ebuild must not wait for the proposed module-per-package
generator. Use a checksummed, release-bound dependency source archive that
builds with network access disabled and matches `go.mod`/`go.sum`. Record how
the archive is generated and verified.

Keep the P4G module-packaging experiment as a later comparison. Promote it only
if it improves reviewability, security ownership, mirror behavior, or
maintenance cost over the locked release archive.

### Automated gates

- `pkgcheck` reports no errors and every warning is fixed or documented.
- Manifest generation is deterministic for an unchanged release input.
- CI detects divergence among the release tag, ebuild version, source digest,
  dependency archive, `go.mod`, and `go.sum`.
- A disposable ROOT installs the package with networking disabled, runs
  `arise --version`, renders the manuals, loads completion syntax, and unmerges
  cleanly.
- The overlay contains no host paths, mutable URLs, private state, or live
  transaction drivers.

### C1 exit

A new Gentoo system can add the overlay and install a released Arise package
without cloning the source repository or allowing network access during build.

## C2 — bounded performance pass

Follow `PERFORMANCE_IMPROVEMENT_PLAN.md` and `../../BENCHMARK_MATRIX.md`.
Correctness equivalence is an admission requirement, not a post-hoc check.

### Baseline

Freeze one repository/profile/VDB/configuration snapshot and record repeated
median and p95 measurements for:

- cold and warm sync/index;
- warm exact search and installed ownership lookup;
- warm explicit-package and no-change plans;
- warm deep/newuse `@world` planning;
- preflight fixed cost for a representative small package.

Record wall time, user/system CPU, peak RSS, allocation profile, bytes
read/written, process count, and normalized Arise/Portage action equivalence.
Use `support/perf/` and machine-readable evidence; do not publish best-of-one
claims.

### Instrumentation

Expose stable spans for configuration/index loading, installed-state graph,
resolver graph construction, solve/verification, plan serialization, and sync
index publication. Human output should name actual phases; JSON output should
retain durations without synthetic motion.

### One optimization

Select the largest measured avoidable cost and implement one bounded change.
The expected first candidate is incremental post-sync index publication rather
than a full rebuild, but the profile decides. Require:

- identical normalized plans and index contents;
- cold, warm, changed, removed, overlay, and interrupted-publication tests;
- no loss of atomic generation rollback;
- a repeated before/after report on the frozen snapshot.

Stop after one accepted optimization. Put further findings into the punch list
rather than extending this cycle.

An incremental installed-linkage index in Badger is a deferred candidate, not
an assumed deliverable. A 2026-07-24 warm comparison found Arise already faster
than Portage for both an empty preserved-rebuild plan (1.477 versus 6.133
seconds) and an exact CMake reinstall plan (1.535 versus 3.250 seconds). Admit
the index only if repeated non-empty profiles make linkage discovery a leading
cost. Cache durable per-CPV ELF facts and publish them atomically; never cache
the transient `@preserved-rebuild` result.

### C2 exit

The repository contains a reproducible baseline, truthful phase timing, and one
measured optimization with unchanged correctness evidence.

## C3 — maintain world

Implement the already-specified `arise maintain world --check` and `--fix`
contract. This is the cycle's maintenance-UX slice; broader `emerge --info`,
dispatch-conf parity hardening, bug-report, and recovery-TUI work stays
separate. The baseline `arise dispatch-conf` command already exists; this plan
does not treat its implemented recursive review workflow as future work.

### Check mode

- Read one immutable repository/profile/VDB/world snapshot.
- Classify malformed, duplicate, moved, unavailable, fully masked, redundant,
  and uninstalled entries.
- Provide deterministic human output, versioned JSON, and meaningful exit
  status.
- Match `emaint --check world` on differential fixtures and the current host's
  stale `sys-fs/udev`, `sys-power/powernowd`, and `x11-base/xorg-x11` atoms.

### Fix mode

- Produce an exact state-bound repair plan before mutation.
- Explain every removal, replacement, or normalization.
- Require saved-plan approval and revalidate beneath the Portage world lock.
- Preserve mode/ownership, publish by atomic rename and directory fsync, and
  retain reversible before/after evidence.
- Fail cleanly on concurrent edits, interruption, ambiguous moves, or a changed
  repository/profile/VDB snapshot.

### C3 exit

The current host passes both Arise and `emaint` world checks, and a clean second
check is idempotent. Disposable-root tests cover corruption, concurrency,
alternate ROOT/PORTAGE_CONFIGROOT, and rollback.

## C4 — stage3 G1

### Reproducible harness

Define a VM recipe with:

- exact architecture and stage3 digest;
- repository snapshot, profile, mirrors, USE, licenses, jobs, and filesystem
  sizing;
- overlay revision and installed Arise package version;
- network boundary, snapshot points, console/log capture, and reboot method;
- frozen plan, binary, state, and configuration hashes.

The overlay is the installation path under test. Local source builds or copied
host binaries are diagnostic only and do not satisfy G1.

### Rehearsal

Run the complete workflow once to validate automation and evidence capture.
Fix harness defects without calling the rehearsal a product failure. Product
defects require portable regressions and a new released overlay package before
the acceptance run.

### Acceptance run

Starting from a newly instantiated, unmodified stage3:

1. add the overlay and install Arise;
2. sync and atomically publish the resolver index;
3. compare and approve a deep/newuse world plan;
4. preflight and execute every build/merge through Arise;
5. run world maintenance, config/news review, journal/resume, VDB, ownership,
   preserved-library, linker, log, and plan-parity checks;
6. exercise representative C/C++, Go, Rust, Python, Perl, LLVM, service, and
   package-query commands;
7. reboot and repeat the health and empty-plan checks.

### C4 exit

Publish immutable G1 evidence proving that Arise maintained and rebooted a fresh
Gentoo stage3. Direct mutation admission was retired earlier after repeated
whole-host install, update, removal, recovery, preserved-library, and world-file
successes; G1 now validates that decision on a clean system. The following cycle
begins with G2 empty-tree planning, not as an implicit extension of this run.

## Cross-cutting rules

- No performance result without normalized correctness equivalence.
- No host-specific mutation drivers in the repository or its maintained
  history.
- First-party shell support uses explicit error handling and never enables
  global `errexit`, `nounset`, or `pipefail`.
- Every external-facing workflow has deterministic text, versioned structured
  output where automation matters, and a documented failure/recovery path.
- Documentation changes ship with the behavior they describe.
- Each phase ends in a small checkpoint commit; do not hold the whole cycle in
  one unreviewable worktree.

## Deliverables

- maintained `arise-overlay` repository with offline install/uninstall CI;
- frozen performance baseline and one accepted optimization report;
- `arise maintain world --check/--fix` plus Portage differential fixtures;
- versioned stage3 VM recipe, rehearsal evidence, and G1 acceptance evidence;
- reconciled README, manuals, compatibility matrix, punch list, and archived
  superseded planning notes.
