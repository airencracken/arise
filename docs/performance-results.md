# Performance

Publish speedups only for equivalent normalized results from identical
repository/profile/configuration/VDB/world inputs. Retain cache state, command
options, tool/source identity, hardware, repeated samples and failed attempts.
The [benchmark format](../misc/PERFORMANCE.md) records wall, CPU, I/O and
process-tree memory without shell-wrapper overhead.

## Search and planning audit, November 17, 2026

| Warm workload | 0.0.55 median | Candidate median |
|---|---:|---:|
| Firefox names only | 0.015 s | 0.017 s |
| dev-python search, 2,067 results | 1.651 s | 0.873 s |
| Nano JSON update preview, zero actions | 1.775 s | 1.480 s |
| Text world update preview, 83 actions | 3.120 s | 2.937 s |
| JSON world update preview, 83 actions | 3.696 s | 3.347 s |
| Synthetic search, 10,000 packages | 12.889 s | 0.790 s |

Each CLI median covers five pairs after one warm-up pair on a Ryzen 7 PRO 4750U
running Gentoo linux/amd64. Each process opened the same local copy of the
November 6 index and read the workstation's installed packages and policy.
The order alternated by pair. Both static binaries used a warm filesystem cache.
Search stdout matched byte for byte. The two text plans differed only in the
reported duration. JSON comparisons included actions, ordering, verification
and approval hashes and excluded only timing and allocation counters.
The separate in-memory Go benchmark used three single-iteration samples per
implementation. The [individual samples and command arguments](evidence/PERFORMANCE_AUDIT_2026-11-17.json)
identify both sources, binaries and index sidecars.

Broad search spent 34% of sampled CPU time populating dependency fields.
The candidate reuses preferred metadata from name lookups and walks recorded
dependency edges to build reverse dependencies. Install planning skips reverse
edges and CPV lookup maps used by graph queries. A JSON preview hashes the
same policy and package state once. Execution still captures the per-file
digests used to detect concurrent changes.

The world solver still took 1.70 s and allocated 329 MB on this workload.
The release profile attributed 34% of sampled CPU time to background garbage
collection. This audit covered warm local searches and previews. Cold startup,
building packages and merging files require separate measurements.

### Comparison with emerge on theshire

| Workload | Arise | emerge | Results (Arise / emerge) |
|---|---:|---:|---|
| Firefox name search | 0.016 s | 0.709 s | 7 packages |
| dev-python/py prefix search | 0.606 s | 2.866 s | 389 packages |
| Nano update preview | 1.768 s | 2.168 s | 0 actions |
| Nano reinstall preview | 1.824 s | 2.202 s | 1 action |
| Full dev-python category | 1.075 s | 11.859 s | 2,067 / 2,068 packages |
| Deep world update preview | 3.820 s | 15.252 s | 83 / 55 actions |

Medians are from five alternating pairs after a warm-up pair per workload.
The audit candidate and Portage 3.0.82.2 read the same profile, world file,
installed database and repositories. Arise used its warm local index.
Portage ran with news bookkeeping disabled. Search comparisons checked full
category/package names. Plan comparisons checked package versions, slots,
repositories, merge types, actions and effective USE flags reported by emerge.

The full category search differed by one installed package,
`dev-python/ensurepip-wheels`, whose ebuild was absent from all the repositories.
Only emerge included it. The 55 shared world actions matched. Arise additionally
selected 28 same-version reinstalls for packages carrying execution ABI 2, 3
or 4. The 0.0.55 worker uses ABI 8. The differing rows show elapsed times for
each command without an equivalent-work speedup claim.

The [paired samples, commands and input revisions](evidence/EMERGE_COMPARISON_2026-11-17.json) include the
two mismatches and the installed ABI of each extra world action.

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
