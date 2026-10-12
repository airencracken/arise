# Benchmark workloads

Compare only equivalent operations with frozen inputs. Use Portage for Gentoo
semantics, eix for indexed queries and portage-utils for native query cost.
Each workload defines its normalizer, cache treatment, isolation and regression
budget before recording speedups. Failed or unequal results cannot satisfy a
performance gate.

| Workload | Reference | Isolation and verification |
|---|---|---|
| Installed CP/CPV, metadata selectors | Portage VDB, eix-installed, qlist | Read-only frozen VDB; sorted identities |
| Exact/substring/empty/installed search | Portage metadata, emerge, eix, qsearch | Disposable index; normalized results and exit status |
| Ownership/files/USE/dependencies | Portage VDB, equery, q tools | Read-only frozen state; complete structured results |
| Full/unchanged/incremental index and sync | Portage, eix-update/eix-sync | Cloned repositories/private indexes; package/repository identity |
| Single/multi-target, system/world, slot conflicts | emerge pretend | Same repository/profile/config/VDB/world; actions, slots, USE and final-state validation |
| Fetch, cold/warm/corrupt/mirror fallback | emerge fetch | Local mirror/private caches; identities and integrity |
| Source/binary install, upgrade and unmerge | emerge | Disposable roots; payload, VDB, world, lifecycle and journal outcomes |
| Parallel dependency DAG | emerge jobs/load | Equivalent options and outputs; queue/commit/critical-path accounting |
| Resume, crash and recovery-set replay | Reference safety contracts | Separate restored roots; failure, retry, rollback and retained evidence |
| Maintenance, news and protected configuration | Portage/Gentoo tools | Read-only or disposable roots; output and mutation parity |

The [performance format](misc/PERFORMANCE.md) and `misc/perf-*.json` files define
executable workloads. [Performance results](docs/performance-results.md) records
dated measurements; this matrix carries no undated host counts or speedups.
Broader cold-cache, resource and system budgets remain in [open work](PUNCHLIST.md).
