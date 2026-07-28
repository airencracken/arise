# C2 target-map preallocation experiment

## Verdict

Rejected. Preallocating five resolver maps from the raw explicit target count
increased memory, allocations, and median latency in the maintained synthetic
world benchmark. The change was removed.

This is a diagnostic result, not the accepted C2 optimization and not evidence
for a live-world performance claim.

## Environment

- revision: `d5f4f6360337eb7ddaba7565efdaa89bc387d909`
- system date: 2026-07-27
- CPU: AMD Ryzen 7 PRO 4750U
- Go benchmark package: `./internal/benchmark`
- workload: `BenchmarkResolveWorld`
- repetitions: five
- correctness boundary: the benchmark executes the ordinary resolver over its
  deterministic 2,000-package fixture and 100 explicit targets

Command:

```sh
env GOCACHE=/tmp/arise-c2-go-cache \
  go test ./internal/benchmark -run '^$' \
  -bench 'BenchmarkResolve(World|Deep)$' -benchmem -count=5
```

## Baseline

`BenchmarkResolveWorld`:

| run | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|
| 1 | 115,527 | 59,520 | 932 |
| 2 | 117,631 | 59,511 | 932 |
| 3 | 114,815 | 59,655 | 932 |
| 4 | 118,331 | 59,639 | 932 |
| 5 | 122,279 | 59,602 | 932 |

Median: 117,631 ns/op, 59,602 B/op, 932 allocs/op.

## Candidate

The candidate gave `rootActionKeys`, `selectedCPs`, `explicitTargets`,
`worldTargets`, and `onlyDepsTargets` an initial capacity equal to the raw
target count and sized the post-expansion target-CP set to the expanded count.

| run | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|
| 1 | 115,543 | 77,052 | 947 |
| 2 | 127,826 | 77,124 | 947 |
| 3 | 144,616 | 77,321 | 947 |
| 4 | 129,361 | 77,187 | 947 |
| 5 | 135,656 | 77,094 | 947 |

Median: 129,361 ns/op, 77,124 B/op, 947 allocs/op.

Relative to the median baseline, the candidate was 10.0% slower and allocated
29.4% more bytes with 15 additional allocations per operation. Raw targets
substantially overestimate membership of several sparse maps, and allocating
all five eagerly costs more than their natural growth.

## Follow-up decision

Do not infer capacities for independent resolver structures from the CLI target
count. The next profile followed live allocation ownership and produced the
accepted shared fingerprint-buffer optimization documented in
[`C2_FINGERPRINT_BUFFER_2026-07-27.md`](C2_FINGERPRINT_BUFFER_2026-07-27.md).
