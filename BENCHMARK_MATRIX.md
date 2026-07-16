# Arise comparison matrix

Arise uses `emerge` as the correctness reference and compares with `eix` or its
companion tools wherever they provide an established fast implementation.
Equivalent behavior is the minimum; every real benchmark also has a speed
floor, and milestone workloads should require a decisive win.

| Area | Benchmark test | Correctness reference | Fast reference | Isolation | Current status |
|---|---|---|---|---|---|
| Installed state | All installed CPVs | Portage VDB | eix-installed | live read-only | measured: pass, 4.53x |
| Installed state | Versionless installed CPs | Portage VDB | eix-installed/qlist | live read-only | workload needed |
| Installed state | Repository/build-time selectors | Portage VDB | eix-installed | live read-only | workload needed |
| Search | Exact CP/name | emerge search/Portage metadata | eix | disposable index | equivalent; 2.85x faster than eix |
| Search | Narrow substring | Portage metadata | eix | disposable index | equivalent; 3.16x faster than eix and 74.22x faster than emerge; 31.00 MiB vs eix 25.80 MiB |
| Search | Broad substring | Portage metadata | eix | disposable index | equivalent; 4.95x faster than eix |
| Search | No-result substring | Portage metadata | eix | disposable index | equivalent exit/output; 3.16x faster than eix |
| Search | Installed/upgradable packages | VDB + emerge plan | eix | disposable index | workload needed |
| Search | Dependency/reverse-dependency query | Portage metadata | eix | disposable index | workload needed |
| Query | File ownership | Portage VDB | equery/qlist | live read-only | workload needed |
| Query | Package file list | Portage VDB | equery/qlist | live read-only | workload needed |
| Query | USE/size/check/which | Portage metadata/VDB | equery | live read-only | workload needed |
| Index | Full repository index | emerge --metadata | eix-update | cloned repo + temp DB | equivalent package set vs eix; 3.27x faster, 30.82 MiB vs 25.77 MiB; privileged emerge workload ready |
| Index | No-change incremental index | emerge --metadata | eix-update | temp DB | equivalent package set; 5.92x faster than eix-update, 30.82 MiB vs 25.77 MiB |
| Index | One-package incremental index | emerge --metadata | eix-update | cloned repo + temp DB | workload needed |
| Sync | No-change sync | emerge --sync | eix-sync | cloned repos | workload needed |
| Sync | Incremental sync | emerge --sync | eix-sync | cloned repos | workload needed |
| Resolution | Single installed package plan | emerge -p | — | immutable snapshot | correctness blocked |
| Resolution | New package dependency plan | emerge -p | — | immutable snapshot | Signal measured: incorrect |
| Resolution | Multi-target plan | emerge -p | — | immutable snapshot | workload needed |
| Resolution | @world plan | emerge -pvuDN @world | — | immutable snapshot | implementation needed |
| Resolution | Backtracking/slot conflict | emerge -p | — | fixture snapshot | workload needed |
| Fetch | Fetch-only, warm DISTDIR | emerge -f | — | temp ROOT/DISTDIR | implementation needed |
| Fetch | Fetch-only, cold DISTDIR | emerge -f | — | local mirror + temp ROOT | implementation needed |
| Build | One trivial source package | emerge | — | isolated ROOT | implementation needed |
| Build | Independent build DAG | emerge --jobs | — | isolated ROOT | implementation needed |
| Binary packages | Local binary install | emerge -K | — | isolated ROOT | implementation needed |
| Binary packages | Remote binhost install | emerge -G | — | isolated ROOT/binhost | implementation needed |
| Install | New package transaction | emerge | — | isolated ROOT | implementation needed |
| Install | Upgrade/reinstall/slot coexistence | emerge | — | isolated ROOT | implementation needed |
| Removal | Uninstall | emerge -C | — | isolated ROOT | implementation needed |
| Removal | Depclean calculation | emerge -pc | — | immutable snapshot | workload needed |
| Removal | Depclean execution | emerge -c | — | isolated ROOT | implementation needed |
| Recovery | Resume after build failure | emerge --resume | — | isolated ROOT | implementation needed |
| Recovery | Crash recovery during merge | Portage safety behavior | — | isolated ROOT | implementation needed |
| Maintenance | Preserved rebuild scan | emerge @preserved-rebuild | revdep-rebuild | isolated/live read-only | workload needed |
| Maintenance | News/config/env queries | Portage tools | eselect/dispatch-conf | isolated ROOT | workload needed |

Timing cells enter the README only from correctness-gated JSON reports. Tasks
that mutate repositories, roots, VDB, world, configuration, or distfiles must
run in cloned or isolated state.
