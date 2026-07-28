# C2 aggressive VDB and graph tuning

## Verdict

Two changes accepted, one rejected:

1. resolver-only VDB scans validate but do not retain `CONTENTS`;
2. dependency metadata streams directly into graph edges;
3. a global final package-policy match cache was rejected.

The final combined median wall time is 2.73 seconds, down 20.9% from the
original 3.45-second C2 baseline. Median CPU is 4.94 seconds, down 36.0% from
7.72 seconds. Median peak RSS is 625,172 KiB, down 23.1% from 812,612 KiB.
Profiled allocation is 747.15 MiB, down 71.8% from 2,645.55 MiB.

Every accepted and rejected candidate preserved the exact verified plan and
state digests.

## Immutable result identity

- plan SHA-256:
  `143d194ae78ba3a9b1a6f7517051be06087808345367ee7f37d43a1a081afef4`
- state SHA-256:
  `4c7a0233cce4d97ceec98ffc67c2fb65978b6cc4b87402a35691e8f67806c8e7`
- verification: whole-state verified
- operation: live read-only deep/newuse complete-graph `@world` update
- execution: five alternating-order warm A/B pairs per accepted candidate

## Rejected final policy-match cache

The candidate cached final results for each `(rule, CPV, slot, repository)`
tuple in a global `sync.Map`. Although keys did not require string
concatenation, the cross-product cardinality and synchronized insertion cost
overwhelmed the saved comparisons.

Median wall time regressed from 2.89 to 5.26 seconds. CPU more than doubled and
peak RSS exceeded 1 GiB. The implementation and its cache-specific tests were
removed completely.

This establishes a useful constraint: future policy acceleration must index
rules by stable package identity or compile rules once. It must not memoize the
complete rule/candidate cross-product.

## Accepted resolver-only VDB projection

### Design

Ordinary `vdb.Scan` continues to load complete `CONTENTS` for ownership and
inspection callers. `ScanResolverState` enforces that `CONTENTS`, `EAPI`,
`SLOT`, and `repository` are regular files in every committed record but omits
the `CONTENTS` payload. Dependency resolution does not inspect installed file
lists.

### Interleaved results

| variant | median wall | median CPU | median peak RSS KiB |
|---|---:|---:|---:|
| full VDB scan | 2.91s | 4.79s | 956,052 |
| resolver projection | 2.82s | 5.27s | 621,764 |
| change | 3.1% faster | 10.0% higher | 35.0% lower |

The higher aggregate CPU is an accepted speed-first tradeoff. Removing the
large retained strings changes garbage-collector scheduling and permits more
concurrent mark work, while the operator-visible critical path and memory
pressure both improve.

Tests prove resolver metadata remains complete, full scans retain `CONTENTS`,
and missing or non-regular `CONTENTS` still excludes partial VDB records.

## Accepted streaming dependency metadata

### Design

`depstring.VisitMeta` emits the same deterministic annotations and order as
`CollectMeta` without recursively constructing slices. Graph construction
consumes the visitor directly, avoiding both the collected metadata slice and
the subsequent edge-pair slice.

### Interleaved results

| variant | median wall | median CPU | median peak RSS KiB |
|---|---:|---:|---:|
| collected metadata | 2.80s | 5.06s | 629,464 |
| streaming metadata | 2.73s | 4.94s | 625,172 |
| change | 2.5% faster | 2.4% lower | 0.7% lower |

The final allocation profile reports 747.15 MiB total allocation. The
previous resolver-projection profile reported 1,027.89 MiB, though allocation
profile variance means the repeated timing comparison remains the primary
speed evidence.

Property tests compare visitor output with `CollectMeta` over atoms, blockers,
USE conditionals, nested any-of groups and REQUIRED_USE cardinality nodes.
Nil nodes and nil visitors fail closed without callbacks.

## Validation

- exact live plan and state digest equivalence;
- unit, property, mutation and adversarial tests;
- full repository test suite;
- Go vet;
- race tests for touched packages;
- project adversarial and mutation-contract lanes;
- formatting and diff checks.

## Next owners

Atom parsing, resolver graph `AddDep`, repository snapshot decoding and reverse
edge construction now dominate allocation. Policy acceleration should use a
compiled per-CP/rule index rather than a result cache. Further graph reduction
must preserve repository multiplicity, any-of identity, conditional
provenance, deterministic ordering and whole-state verification.
