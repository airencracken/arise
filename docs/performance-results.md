# Performance

Publish speedups only for equivalent normalized results from identical
repository/profile/configuration/VDB/world inputs. Retain cache state, command
options, tool/source identity, hardware, repeated samples and failed attempts.
The [benchmark format](../misc/PERFORMANCE.md) records wall, CPU, I/O and
process-tree memory without shell-wrapper overhead.

## 0.0.42 stabilization measurements

| Warm workload | Arise median | Portage median | Speedup |
|---|---:|---:|---:|
| Nano update, one action | 1.584 s | 6.032 s | 3.81x |
| Coreutils build-only reinstall, one action | 1.597 s | 3.831 s | 2.40x |

Five paired runs after one warmup used frozen inputs on one Gentoo workstation;
both cases required action parity. These are workload measurements, not general
performance guarantees. Cold-cache and broader system budgets remain open.

The separate [world-update audit](audits/WORLD_UPDATE_AUDIT_2026-10-11.md)
reduced a blocker-loop plan from over four minutes to 5.29–5.33 seconds versus
Portage's 28.38–28.63 seconds. Both final plans passed independent validation;
Arise retained four additional required Perl rebuilds, so this is classified
plan divergence rather than an identical-action speedup claim.

## Method and boundaries

Resolver normalization includes CPV, slot/subslot, repository, action, merge
type and effective USE. Query/index workloads compare sorted results. Rejected
or incomplete commands are failure evidence, never successful timing samples.
Read-only Portage comparisons disable news bookkeeping. Separate warm, cold
and incremental trials; define budgets and noise before optimizing.

Dependency-aware parallel preparation with serialized commits is implemented,
as are source installed lifecycle execution, durable journals and fault-injected
recovery. General Portage execution parity is not claimed. Required binary
lifecycle hooks remain an explicit source-fallback/rejection boundary.

The [0.0.42 review](reviews/2026-10-11-stabilization-release.md) records scope.
The [0.0.7 measurements](evidence/RELEASE_0.0.7_PERFORMANCE_2026-07-29.json)
remain historical evidence, not current release numbers. See the
[workload matrix](../BENCHMARK_MATRIX.md) and [remaining work](../PUNCHLIST.md).
