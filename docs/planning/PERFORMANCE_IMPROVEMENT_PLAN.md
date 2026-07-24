# Arise performance improvement plan

## Purpose

Arise should make package-manager overhead materially smaller than Portage
without weakening plan equivalence, phase compatibility, transaction safety,
or recovery guarantees. Compilation will dominate large source builds, so a
single empty-tree wall-clock number is not sufficient. The performance program
must separately measure interactive latency, package-manager CPU work,
filesystem durability, worker overhead, scheduling efficiency, and sustained
throughput.

This document defines the profiling and optimization process to begin after the
current correctness run completes and profiling tools, including `perf`, can be
installed cleanly. [`../../BENCHMARK_MATRIX.md`](../../BENCHMARK_MATRIX.md)
remains the authoritative workload and comparison inventory. This plan defines
how performance work is conducted.

## Non-negotiable constraints

Every result is correctness-gated. An optimization is invalid if it changes or
omits required work merely to improve timing.

Each comparison must preserve or record, as applicable:

- repository commit and configuration identity;
- profile, package masks, keywords, licenses, USE flags, and dependency roots;
- selected CPVs, slots, repositories, actions, and dependency edges;
- approved and verified plan SHA-256 values;
- distfile identities and fetch state;
- final VDB metadata and installed payload identity;
- journal outcome, lifecycle outcome, and resume state;
- normalized reference output and equivalence digest.

Performance claims use repeated measurements and report medians and tail
latency. Best-of-one timings are diagnostic only. Failed or non-equivalent runs
do not contribute performance claims.

## Questions the profiling must answer

The first profiling campaign should answer these questions before code is
optimized:

1. How much latency is spent before a plan is available?
2. How much repository, configuration, and VDB work is repeated unnecessarily?
3. How much time is lost starting Bash workers and constructing their
   environments?
4. Are runnable packages waiting despite available CPU and memory?
5. Are fetch verification, build execution, and merge work overlapping well?
6. How long do packages wait for the commit/VDB lock?
7. Which journaling operations, ownership scans, hashes, compression calls, or
   durability barriers dominate merge latency?
8. How much CPU time and allocation pressure comes from parsing, graph cloning,
   string manipulation, event encoding, and logging?
9. Are caches saving work, or merely shifting cost into database reads,
   validation, and garbage collection?
10. Which costs are fixed per invocation, fixed per package, proportional to
    repository/VDB size, or proportional to installed payload size?

## Workload matrix

### Interactive and fixed-cost workloads

These expose startup and package-manager overhead that compilation-heavy runs
hide:

- no-argument/help/version startup;
- no-change invocation with every dependency already satisfied;
- cold and warm single-package `--pretend`;
- cold and warm large `@world` pretend plans;
- exact, narrow, broad, and no-result searches;
- installed-package listing and ownership queries;
- one trivial package install and reinstall;
- one warm-cache package whose build phase is effectively free.

### Throughput workloads

These expose scheduling, worker, merge, and per-package costs:

- hundreds of independent trivial packages;
- a layered synthetic DAG with known critical path and fan-out;
- mixed short and long builds to expose tail scheduling behavior;
- warm-distfile, warm-compiler-cache package sets;
- a full empty-tree `@system` run;
- a full empty-tree `@world` run;
- repeated real-world update plans with the same frozen snapshot.

### Transaction and recovery workloads

These measure safety costs directly:

- new install, reinstall, version upgrade, and slot-coexistence merge;
- payloads with many small files, large files, symlinks, and hardlinks;
- CONFIG_PROTECT, preserve-libs, lifecycle, Info-index, and install-QA cases;
- intentional failure before merge, during journaled merge, after commit, and
  during resume;
- recovery from every durable journal boundary;
- parallel builds converging on the serialized commit path.

### Fetch workloads

- cold DISTDIR using a controlled local mirror;
- warm DISTDIR with all hashes valid;
- one corrupt or stale distfile;
- many small files versus a few large files;
- mirror failure and fallback;
- fetch concurrency while build slots are active.

## State controls

Every report must label the state of each relevant cache:

| State | Cold condition | Warm condition |
|---|---|---|
| Kernel page cache | explicitly dropped only in an isolated, approved environment | warmed by a defined untimed pass |
| Arise metadata/Badger | newly created database | unchanged validated database |
| Repository metadata | uncached or cloned fixture | unchanged repository and index |
| VDB | disposable frozen copy | same copy after an untimed scan |
| Distfiles | empty disposable directory | fully verified directory |
| Compiler cache | disabled or empty isolated cache | fixed populated cache |
| Build directories | absent | explicitly retained only for a named workload |
| Resume/journal state | absent | prepared fixture for recovery workloads |

Do not drop the host page cache during ordinary development or live package
operations. Cold-cache runs belong in a disposable test environment with an
explicitly recorded setup.

## Instrumentation to add first

Add low-overhead, monotonic-clock spans with a stable schema. Instrumentation
must be disabled or inexpensive by default and export machine-readable events.

### Invocation spans

- process startup and argument/configuration parsing;
- repository discovery and configuration loading;
- metadata database open, validation, and close;
- VDB snapshot construction;
- resolver graph construction, solve, backtracking, and explanation;
- plan serialization, hashing, authorization, and locked revalidation;
- total preflight and execution time.

### Package spans

- ready-queue delay and load-average throttle delay;
- fetch queue, download, and hash verification;
- work-directory and worker preparation;
- worker process startup and ebuild/eclass sourcing;
- each ebuild phase;
- install-QA and image finalization;
- wait for commit lock;
- ownership/collision preflight;
- journal creation, capture, mutation, sync, commit, and rollback;
- native ELF metadata generation;
- VDB metadata creation and replacement cleanup;
- package lifecycle and generated-cache maintenance;
- log persistence and compression.

### Counters and distributions

- packages ready, running, blocked, committed, and failed over time;
- worker processes and dynamic phase retries;
- files/directories walked, stat calls requested, and bytes hashed;
- VDB entries and CONTENTS records scanned;
- journal entries and bytes copied;
- database gets, misses, writes, and iterator steps;
- allocations and bytes allocated by pipeline stage;
- lock wait and hold-time histograms;
- phase, fetch, and merge latency histograms;
- scheduler idle time while runnable package work exists.

Each event should include a run identifier, plan digest, package/action identity
where relevant, stage name, start/end or duration, outcome, and concurrency
level. Avoid package output or timestamps that make deterministic comparison
needlessly difficult.

## Profiling toolkit

Use multiple complementary views. No single profile distinguishes CPU work,
I/O waits, process startup, and lock contention reliably.

### Top-level accounting

- `/usr/bin/time -v` for wall time, CPU time, RSS, faults, and context switches;
- `perf stat` for cycles, instructions, IPC, branches, cache misses, migrations,
  context switches, page faults, and task-clock;
- Arise span summaries for stage and package attribution.

### CPU and memory

- `perf record` with call graphs for whole-process and child-process CPU;
- Go CPU, heap, allocation, mutex, and block profiles;
- Go execution traces for goroutine scheduling and stop-the-world behavior;
- flame graphs and differential flame graphs between revisions.

### I/O, syscalls, and off-CPU time

- `strace -f -c` for syscall and child-process summaries;
- targeted `strace` traces for `openat`, `statx`, `read`, `write`, `rename`,
  `unlink`, `fsync`, `fdatasync`, `clone`, `execve`, and waits;
- `perf sched` or equivalent off-CPU analysis;
- eBPF/bpftrace latency distributions for filesystem, block I/O, scheduler,
  and lock-related events when supported;
- block-device statistics for write amplification and queue latency.

Profiles must record tool versions, kernel, CPU governor, storage, filesystem,
mount options, Go version, build flags, GOMAXPROCS, job count, load limit, and
relevant Portage/Arise configuration.

## Suspected hot paths

These are hypotheses to test, not conclusions.

### Startup, ingestion, and caches

- repeated config/profile/repository parsing;
- recursive directory walking and redundant `stat` calls;
- decoding metadata not needed by the requested command;
- Badger open/validation/close cost and undersized caches;
- full scans where immutable side indexes could answer the query;
- hashing unchanged repository/configuration state more than once;
- excessive string conversion, map cloning, and short-lived allocations.

### Resolver and plan construction

- repeated atom matching and version sorting;
- repeated USE/dependency expression evaluation;
- cloning complete candidate or IUSE maps for read-only operations;
- graph construction that is not shared between validation passes;
- conflict/backtracking work repeated after an equivalent state check;
- explanation generation on successful paths.

### Workers and phases

- one Bash and sandbox process startup per phase or package;
- large environment serialization and repeated eclass sourcing;
- dynamic `has_version` discovery retries;
- protocol JSON encoding/decoding and line-by-line flushing;
- redundant filesystem setup and teardown;
- install-QA checks that rescan the complete image independently.

Worker reuse or batching must never leak shell variables, phase state, file
descriptors, working directories, or package privileges between packages.

### Fetch and verification

- hashing a warm verified distfile repeatedly;
- small buffers or excessive seeks;
- mirror fallback head-of-line blocking;
- fetch workers that do not overlap with build work;
- contention on shared progress output or distfile bookkeeping.

Any verification cache must be keyed by enough immutable identity to prevent a
changed file from bypassing hashing.

### Merge, VDB, and durability

**Testing blocker — per-entry journal synchronization.** A live
`sys-kernel/gentoo-sources-7.1.3` merge on 2026-07-21 exposed approximately
99,858 image paths. The version-3 journal appended and called `fsync` for every
captured path. At 80,939 entries it was advancing at only about 80 paths per
second, turning a package with a 56-second historical estimate into a
20-minute-class transaction. This is unacceptable both as wall-clock overhead
and as filesystem-journal/SSD write amplification. Pause large world and
empty-tree mutation testing until the portable transaction backend provides:

- subtree coalescing when a previously absent directory contains only newly
  installed descendants;
- bounded write-ahead batches for mixed or replacement trees, with one durable
  barrier before mutating that batch;
- a long-lived segmented journal writer instead of open/write/sync/close per
  entry;
- rate-limited capture/install progress and separate durability timing;
- crash-injection proof at every new batch boundary; and
- a kernel-source regression benchmark that reports sync calls, bytes written,
  write amplification, journal time, merge time, rollback time, and total time.

The optimized path must preserve the rule that no mutation may precede a
durable description of its preimage. Per-entry synchronous mode may remain as
an explicit diagnostic backend, but is not suitable as the production default.

Initial remediation on 2026-07-21 introduced version-4 absent-subtree records,
a two-pass preimage batch published before mutation, grouped backup durability,
and one payload-filesystem `syncfs` before commit. The isolated 1,000-file
rollback fixture fell from 1,015 forced sync calls and 3.74 seconds to 11 fixed
`fsync` calls and approximately 0.39 seconds. A successful transaction shows
10 fixed journal/state `fsync` calls plus one grouped `syncfs`. This closes the
linear forced-sync mechanism in the initial synthetic fixture. A larger-tree
benchmark and process-death coverage at the group boundaries were retained as
the final testing-blocker checks.

The follow-up gate completed the same day. Real SIGKILL subprocess tests now
recover and retry at three boundaries: after the preimage group is durable but
before mutation, after mutation but before payload sync, and after payload sync
but before journal commit. Under `strace`, a 20,000-file rollback transaction
performs 11 fixed `fsync` calls; the committed form performs the same 11 plus
one grouped `syncfs`. Durability barriers therefore remain constant as path
count grows, and large-world testing may resume. A live kernel-source timing is
still required as release-performance evidence, not as a testing blocker.

- rebuilding the global ownership map for each package;
- repeated VDB CONTENTS parsing and path normalization;
- walking the staged image multiple times for collisions, metadata, merge, and
  QA;
- per-file journal copies where reflinks, links, or grouped operations are safe;
- excessive directory syncs or durability barriers;
- serial native ELF parsing;
- log/environment compression on the critical path;
- long commit-lock hold time caused by work that could happen before locking;
- generated-cache maintenance repeated once per package instead of coalesced
  safely at transaction boundaries.

Durability optimizations require crash-injection evidence at every affected
boundary. Fewer `fsync` calls are not an improvement unless the recovery
contract remains true.

### Scheduling and output

- ready work left idle by conservative load throttling;
- dependency-ready propagation latency;
- a single commit lane blocking unrelated pre-commit work;
- poor balance between memory-heavy and CPU-heavy builds;
- output locks, formatting, colorization, and synchronous flushing;
- cancellation that waits unnecessarily for unrelated workers.

## Optimization order

Optimize in descending order of measured end-to-end impact:

1. Remove duplicated whole-repository, VDB, or staged-image work.
2. Eliminate avoidable serialization and scheduler idle gaps.
3. Move safe work outside the commit lock.
4. Reduce worker/process startup and environment construction cost.
5. Improve immutable indexes and cache lookup behavior.
6. Reduce allocations and parsing work in resolver and ingestion paths.
7. Batch or defer non-critical compression and generated-cache maintenance
   where compatibility permits.
8. Tune buffers, database caches, and concurrency only after the responsible
   bottleneck is demonstrated.
9. Apply micro-optimizations only when profiles show they affect a maintained
   workload.

Do not begin with cache-size tuning merely because a cache emits a warning.
Measure hit rate, eviction lifetime, memory cost, and end-to-end effect first.

## Experiment protocol

For every proposed optimization:

1. Select one named workload and state the hypothesis.
2. Capture a correctness-gated baseline from a clean revision.
3. Save top-level counters and at least one profile that demonstrates the cost.
4. Make one logically isolated change.
5. Re-run correctness checks and the selected workload under identical state.
6. Compare median, p90/p95 where practical, CPU time, system time, allocations,
   I/O, RSS, and the targeted span—not only wall time.
7. Run the broader workload matrix to detect regressions or cost shifting.
8. Store machine-readable evidence and document the conclusion, including a
   null or negative result.

Use enough repetitions to overcome noise. Short commands generally need more
samples than long builds. Randomize Arise/reference execution order when
thermal state or cache warming could bias results.

## Acceptance criteria

An optimization may be retained when:

- all equivalence and transaction-safety gates pass;
- the target workload improves beyond expected measurement noise;
- no maintained workload suffers an unexplained material regression;
- memory, disk usage, and write amplification remain within an explicit budget;
- tail latency does not regress merely to improve the median;
- new caches have versioning, validation, invalidation, and crash-safe publish
  behavior;
- concurrency changes remain race-free and preserve deterministic plans;
- the evidence is reproducible from documented commands or fixtures.

Initial budgets should be established from measured baselines rather than
invented prematurely. Once stable, promote key budgets into automated
performance gates.

## Empty-tree campaign

Empty-tree merges are a major end goal, but should come after fixed-cost and
synthetic-DAG instrumentation is trustworthy.

For each empty-tree campaign:

- freeze repository, configuration, world, VDB, distfiles, and compiler-cache
  identity;
- record the complete plan and critical path;
- capture package start/build/install/commit timelines;
- plot active build slots, runnable packages, load, memory, fetch activity, and
  commit-lock occupancy over time;
- identify idle gaps on the critical path;
- separate package-manager CPU from compiler/linker CPU;
- compare Arise and emerge with equivalent job/load settings and outputs;
- repeat warm-distfile and warm-compiler-cache variants to amplify manager
  overhead;
- retain failure/resume evidence rather than discarding interrupted runs.

The desired result is not only a lower total time. Arise should show lower
manager CPU overhead, faster readiness decisions, better slot utilization, and
lower fixed per-package latency while preserving stronger transaction evidence.

## Deliverables

1. Stable span/event schema and trace export.
2. Reproducible fixture and live-read-only workload definitions.
3. A runner that captures correctness digests, state identity, timing, profiles,
   and tool versions together.
4. Baseline reports for interactive, tiny-package, synthetic-DAG, and
   empty-tree workloads.
5. Flame graphs, off-CPU views, lock histograms, and filesystem latency data for
   the initial bottlenecks.
6. One evidence record per accepted optimization.
7. Updated benchmark matrix and README claims only after correctness-gated,
   reproducible results exist.

## First profiling session checklist

After the current correctness run and clean installation of profiling tools:

1. Record the host/toolchain/configuration manifest.
2. Build an optimized Arise binary with profile labels enabled.
3. Measure warm no-op, warm search, and warm large pretend workloads.
4. Measure one trivial reinstall and a small independent package set.
5. Capture `perf stat`, Go CPU/heap/mutex/block profiles, and `strace -f -c`.
6. Run a synthetic DAG and inspect ready-queue and commit-lock timelines.
7. Rank costs by potential end-to-end savings.
8. Choose the first optimization only after the profiles agree on the limiting
   stage.

The expected early contrast is useful: interactive and tiny-package workloads
should expose Python-style fixed overhead that Arise is designed to avoid,
while empty-tree runs should expose scheduler utilization, repeated per-package
work, and commit serialization hidden beneath compilation.
