# Arise comparison matrix

Arise uses `emerge` as the correctness reference and compares with `eix` or its
companion tools wherever they provide an established fast implementation.
Equivalent behavior is the minimum. Emerge/eix milestone workloads have a
build-breaking speed floor and should require a decisive win. Portage-utils is
an aspirational native-speed target: its results are published honestly, but a
loss does not fail the build.

| Area | Benchmark test | Correctness reference | Fast reference | Isolation | Current status |
|---|---|---|---|---|---|
| Installed state | All installed CPVs | Portage VDB | eix-installed | live read-only | measured: equivalent, 5.83x |
| Installed state | Versionless installed CPs | Portage VDB | eix-installed/qlist | live read-only | workload needed |
| Installed state | Repository/build-time selectors | Portage VDB | eix-installed | live read-only | workload needed |
| Package state | All available and installed CPVs | Portage Python API | — | live read-only | measured: exact, 36,111 available and 1,233 installed |
| Installed state | All installed CPVs | Portage VDB | qlist | live read-only | workload added; native-speed floor |
| Installed state | Versionless installed CPs | Portage VDB | qlist | live read-only | workload added; native-speed floor |
| Search | Exact CP/name | emerge search/Portage metadata | eix | disposable index | equivalent; 2.56x faster than eix |
| Search | Narrow substring | Portage metadata | eix | disposable index | equivalent; 2.90x faster than eix and 79.03x faster than emerge |
| Search | Broad substring | Portage metadata | eix | disposable index | equivalent; 4.98x faster than eix |
| Search | No-result substring | Portage metadata | eix | disposable index | equivalent exit/output; 3.31x faster than eix |
| Search | Installed/upgradable packages | VDB + emerge plan | eix | disposable index | workload needed |
| Search | Dependency/reverse-dependency query | Portage metadata | eix | disposable index | workload needed |
| Query | File ownership | Portage VDB | equery/qlist | live read-only | workload needed |
| Query | Package file list | Portage VDB | equery/qlist | live read-only | workload needed |
| Query | USE/size/check/which | Portage metadata/VDB | equery | live read-only | workload needed |
| Query | Atom parse/reconstruct/compare | PMS/Portage vercmp | qatom | synthetic corpus | Arise CLI surface and workload needed |
| Search | Native tree name/description search | Portage metadata | qsearch | isolated repos.conf | workload needed |
| Query | Native ownership/files/deps/size/USE/check | Portage VDB | qfile/qlist/qdepends/qsize/quse/qcheck | live read-only | workloads needed |
| Index | Full repository index | emerge --metadata | eix-update | cloned repo + temp DB | equivalent configured-repository names; crash-safe generation measured at 3.96s vs 4.26s (1.08x) |
| Index | No-change index | emerge --metadata | eix-update | temp DB | equivalent configured-repository names; crash-safe incremental generation measured at 1.86s vs 4.26s (2.29x) |
| Index | One-package incremental index | emerge --metadata | eix-update | cloned repo + temp DB | workload needed |
| Sync | No-change sync | emerge --sync | eix-sync | cloned repos | workload needed |
| Sync | Incremental sync | emerge --sync | eix-sync | cloned repos | workload needed |
| Resolution | Single installed package plan | emerge -p | — | immutable snapshot | workload needed |
| Resolution | New package dependency plan | emerge -p | — | immutable snapshot | Signal action/USE plan equivalent; 1.30s vs 3.30s (2.54x) |
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
