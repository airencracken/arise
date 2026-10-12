# Performance work

Profile before optimizing. Use [the workload matrix](../../BENCHMARK_MATRIX.md)
and [methodology](../../misc/PERFORMANCE.md); retain exact normalized output
alongside latency, CPU, allocations, process-tree memory, I/O and tail samples.

Freeze repository, profile/configuration, VDB/world, roots, source/binary/cache
state, jobs/load and output modes. Measure startup and no-op cost separately
from indexing, solving, validation, fetch, phase work, commit and recovery.
Attribute queue/lock waits, repeated scans/hashes, sync calls and child-process
cost before adding caches or concurrency.

Non-Gentoo microbenchmarks are development evidence only. An authoritative
comparison requires a Gentoo execution host, identified hardware/toolchains and
the same frozen inputs. Separate cold, warm-distfile and warm-compiler-cache
trials with repeated samples. CachyOS workstation measurements cannot satisfy
this gate.

Optimization must preserve package/domain/USE/action identity, payload/VDB,
approval and journal outcomes. New caches need versioning, validation,
invalidation and atomic publication; durability batching needs crash injection.
Define noise bands and regression budgets before interpreting speedups. Reject
unequal outcomes, unexplained tail regressions or costs shifted to unmeasured work.

Acceptance: reproducible equivalent workload improvement beyond noise, bounded
memory/disk/write amplification, race-free behavior and unchanged complexity
ratchets. Broader cold/system budgets remain open after the 0.0.42 warm samples.
