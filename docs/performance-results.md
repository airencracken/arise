# Performance results

`emerge` is the behavioral reference. `eix`, `eix-installed`, `equery`, and
related tools are performance references where they provide an equivalent
operation. Arise's benchmark gate rejects correctness-equivalent regressions
and requires a material measured benefit before making a performance claim.

## Gentoo tool comparisons

Same-snapshot, correctness-gated checkpoint results through 2026-07-25:

| Task | Reference | Equivalent | Arise median | Reference median | Speedup |
|---|---|---:|---:|---:|---:|
| List all installed CPVs | eix-installed | yes | 6.42 ms | 37.47 ms | **5.83x** |
| Firefox substring search | eix | yes | 11.69 ms | 33.95 ms | **2.90x** |
| Firefox substring search | emerge | yes | 10.95 ms | 865.47 ms | **79.03x** |
| Shallow `@system` update plan | emerge | yes (11/11 actions) | 1.43 s | 6.48 s | **4.52x** |
| Deep/newuse `@world` pretend plan | emerge | yes (1/1 action) | 2.88 s | 17.48 s | **6.07x** |
| Crash-safe full configured-repository index | eix-update | yes | 3.96 s | 4.26 s | **1.08x** |
| Crash-safe no-change configured-repository index | eix-update | yes | 1.86 s | 4.26 s | **2.29x** |

The `@world` row is the median of three uninstrumented warm runs on the live
x86-64 workstation after synchronizing all configured repositories. Both
resolvers selected only `dev-util/codex-0.145.0::guru` with the same USE state.
The exact commands were `./arise --pretend --update --deep --newuse @world` and
`FEATURES=-news emerge --pretend --verbose --color=n --backtrack=20 --update
--deep --newuse @world`; disabling Portage's news side effect kept the
comparison read-only.

Process-tree memory was measured separately by polling Linux `smaps_rollup`
every 10 ms. The sampler includes all descendants and reports PSS and USS; its
overhead is excluded from the headline latency row.

| Cache state | Instrumented median wall (Arise / emerge) | Median PSS (Arise / emerge) | Median USS (Arise / emerge) |
|---|---:|---:|---:|
| Warm | 5.32 s / 19.58 s | 912.02 MiB / 239.47 MiB (**3.81x**) | 912.02 MiB / 237.88 MiB (**3.83x**) |
| Cold | 12.00 s / 20.87 s | 939.07 MiB / 239.51 MiB (**3.92x**) | 939.07 MiB / 237.90 MiB (**3.95x**) |

Cold runs call `sync` and drop Linux page, dentry, and inode caches before each
command, alternating execution order. Those measurements identified the
snapshot and graph allocations subsequently reduced by the 0.0.5 C2 cycle.
Commands, samples, binary digest, repository commits, and correctness results
are preserved in
[`evidence/WORLD_PRETEND_PERFORMANCE_2026-07-25.json`](evidence/WORLD_PRETEND_PERFORMANCE_2026-07-25.json).

## Damaged-state recovery diagnostic

Damaged-state recovery is reported separately because unequal outcomes cannot
enter the equivalence speedup table. On repository commit
`dbc31827cd0aab0d3b90114899a2eb2136dcb726`, three uninstrumented runs of deep,
newuse, complete-graph `@system` with build dependencies produced:

| Resolver | Outcome | Actions | Median wall time |
|---|---|---:|---:|
| Arise | verified repair, zero conflicts | 159 | **2.05 s** |
| Portage 3.0.77 | unresolved partial plan, slot conflicts and four unsatisfied blocks | 143 displayed | 27.34 s |

Arise used 13.33x less wall time while producing the stronger verified
outcome. This is a recovery diagnostic, not a Portage-equivalent speedup.
Evidence is preserved in
[`evidence/P3_SYSTEM_REPAIR_TIMINGS_2026-07-18.json`](evidence/P3_SYSTEM_REPAIR_TIMINGS_2026-07-18.json).

The index comparison covers every configured repository and validates
normalized package names after each build. Its dated samples are preserved in
[`evidence/P3_SYSTEM_SHALLOW_TIMINGS_2026-07-18.json`](evidence/P3_SYSTEM_SHALLOW_TIMINGS_2026-07-18.json).

## Claim boundaries

- Dependency-aware parallel preparation with serialized commits, atomic
  Manifest-verified fetching, installed lifecycle execution, durable journals,
  resume recovery, and fault-injected atomicity tests are implemented.
- General Portage execution parity is still not claimed. It requires broader
  machine, EAPI, package, filesystem-layout, failure, and long-running upgrade
  corpora.
- The isolated `emerge --metadata` workload and the 369-action 2026-07-18
  deep/newuse/complete-graph `@world` snapshot remain unpublished because they
  have not completed the repeated correctness-gated benchmark protocol.
- The current damaged `@system` outcome remains ineligible for the equivalence
  table because Arise repairs the state while Portage reports a partial plan.
- Signal Desktop remains a binary-package and user-patch regression, not a
  representative resolver benchmark.

See [`../BENCHMARK_MATRIX.md`](../BENCHMARK_MATRIX.md) for the workload matrix,
[`../misc/PERFORMANCE.md`](../misc/PERFORMANCE.md) for methodology, and
[`releases/0.0.5.md`](releases/0.0.5.md) for the latest release result.
