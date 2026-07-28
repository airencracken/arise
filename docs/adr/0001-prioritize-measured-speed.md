# ADR-0001: Prioritize measured speed in performance-critical paths

## Status

Accepted on 2026-07-28.

## Context

Arise exists in part to make Gentoo package-management planning, querying, and
repository operations faster. Some optimizations exchange additional bounded
memory, binary size, or dependency storage for lower latency and greater
throughput. Optimizing those secondary costs without measuring user-visible
runtime can make the main product goal worse.

This is not permission for unbounded allocation. Recovery workloads must still
operate predictably on constrained systems, and memory regressions can become
correctness failures through exhaustion.

The maintained [`BENCHMARK_MATRIX.md`](../../BENCHMARK_MATRIX.md) defines
comparison workloads and claim boundaries.
[`performance-results.md`](../performance-results.md) records maintained
results and methodology. The graph-optimization records under
[`docs/evidence`](../evidence/) include accepted and rejected
allocation/performance experiments.

## Decision

For measured performance-critical paths, prefer lower wall-clock latency and
higher throughput over reducing memory, binary size, or build-time dependency
storage when all of the following hold:

1. Correctness and recovery guarantees remain unchanged.
2. Resource growth is measured, bounded, and acceptable for supported systems.
3. The speed improvement is reproduced on representative workloads.
4. The optimization does not introduce an operational dependency that weakens
   the recovery control plane without a functional fallback.

Performance changes must report both time and relevant resource costs. A change
is not accepted merely because it is theoretically faster or uses less memory.

## Consequences

- Hot-path caches, preallocation, immutable snapshots, and in-process
  implementations may intentionally consume more memory or artifact space.
- Dependency archive size and compile time are optimization targets, but they
  do not override a demonstrated runtime advantage by themselves.
- Benchmarks require correctness gates and comparable inputs.
- Material memory growth remains a regression when it threatens supported
  recovery workloads or lacks a demonstrated runtime benefit.

A superseding ADR is warranted if supported recovery targets receive explicit
memory budgets, representative benchmarks show that memory pressure dominates
wall time, or deployment constraints make binary or dependency size a primary
product requirement.
