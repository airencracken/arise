# Performance results

`emerge` is the behavioral reference. `eix`, `eix-installed`, and related tools
are performance references where they provide an equivalent operation. Arise
publishes a speedup only when normalized results from the same system snapshot
are equivalent.

## Gentoo tool comparisons

Release-candidate results captured on 2026-07-29:

| Task | Arise median | Reference | Reference median | Arise speedup |
|---|---:|---|---:|---:|
| List all installed CPVs | 8.54 ms | `eix-installed` | 38.22 ms | **4.48x** |
| Firefox substring search | 14.60 ms | `eix` | 35.05 ms | **2.40x** |
| Firefox substring search | 13.66 ms | `emerge` | 960.59 ms | **70.34x** |
| Shallow `@system` update plan | 1.96 s | `emerge` | 3.78 s | **1.93x** |
| Deep/newuse `@world` pretend plan | 3.25 s | `emerge` | 17.98 s | **5.54x** |
| Full configured-repository index | 7.14 s | `eix-update` | 10.55 s | **1.48x** |
| No-change configured-repository index | 3.79 s | `eix-update` | 10.52 s | **2.78x** |

Every row passed the workload's output normalizer. Resolver rows compare CPV,
slot, repository, action, merge type, and effective USE rather than relying on
exit codes. Index rows validate the resulting normalized package-name set.
Query rows compare normalized sorted results.

The resolver release gate now requires at least a 1.2x median lead. Regular
tests also exercise a large deterministic final-state lookup budget and assert
that policy evaluation is confined to selected actions. These gates were added
after independent validation briefly caused a major pre-release regression.

The exact samples, tool versions, snapshot identities, binary digest, and
performance floors are preserved in
[`evidence/RELEASE_0.0.7_PERFORMANCE_2026-07-29.json`](evidence/RELEASE_0.0.7_PERFORMANCE_2026-07-29.json).

## Method

The harness runs each tool directly without a shell, records repeated wall,
CPU, I/O, and process-tree memory samples, and rejects unequal normalized
output before considering speed. Resolver comparisons disable Portage's news
side effect with `FEATURES=-news` so both commands remain read-only.

The current rows are warm-cache comparisons. Full index runs remove only their
explicit benchmark paths before every sample; no-change index runs retain the
previous index. See [`../misc/PERFORMANCE.md`](../misc/PERFORMANCE.md) for the
workload schema, normalization rules, cache handling, and benchmark policy.

## Claim boundaries

- Dependency-aware parallel preparation with serialized commits, atomic
  Manifest-verified fetching, installed lifecycle execution, durable journals,
  resume recovery, and fault-injected atomicity tests are implemented.
- General Portage execution parity is not claimed. It requires broader machine,
  EAPI, package, filesystem-layout, failure, and long-running upgrade corpora.
- Results are measurements from one x86-64 Gentoo workstation and are not
  universal performance guarantees.
- Damaged-state recovery results remain separate because unequal outcomes
  cannot enter the equivalence table.

See [`../BENCHMARK_MATRIX.md`](../BENCHMARK_MATRIX.md) for the workload matrix
and [`releases/0.0.7.md`](releases/0.0.7.md) for release context.
