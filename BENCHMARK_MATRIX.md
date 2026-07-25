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
| Resolution | New package dependency plan | emerge -p | — | immutable snapshot | explicit-package matrix verified; current same-snapshot timing publication pending |
| Resolution | Multi-target plan | emerge -p | — | immutable snapshot | workload needed |
| Resolution | Shallow @system plan | emerge -pu @system | — | live read-only snapshot | exact 11/11 across five runs; 1.43s vs 6.48s (4.52x) |
| Resolution | Deep @system plan | emerge -puDN @system | — | immutable snapshot | damaged-state diagnostic: verified 159-action Arise repair 2.05s median vs unresolved 143-action Portage partial 27.34s; not an equivalence speedup |
| Resolution | Standard @world plan | emerge -puDN @world | — | immutable snapshot | measured: equivalent 1/1 action; uninstrumented warm 2.88s vs 17.48s (6.07x); instrumented warm PSS 912.02 vs 239.47 MiB (3.81x); cold PSS 939.07 vs 239.51 MiB (3.92x) |
| Resolution | Recovery @world plan | emerge -puDN --keep-going --with-bdeps=y --complete-graph --backtrack=1000 @world | — | immutable snapshot | correctness-gated workload added; current deep complete-graph diagnostic is 313 vs 317 actions |
| Resolution | Backtracking/slot conflict | emerge -p | — | fixture snapshot | workload needed |
| Fetch | Fetch-only, warm DISTDIR | emerge -f | — | temp ROOT/DISTDIR | verified fetch implementation/tests exist; comparison workload needed |
| Fetch | Fetch-only, cold DISTDIR | emerge -f | — | local mirror + temp ROOT | mirror-aware verified fetch tests exist; comparison workload needed |
| Build | One trivial source package | emerge | — | isolated ROOT | disposable-root source-build tests exist; timed comparison workload needed |
| Build | Independent build DAG | emerge --jobs | — | isolated ROOT | dependency-aware scheduler tests/live use exist; deterministic comparison workload needed |
| Binary packages | Local binary install | emerge -K | — | isolated ROOT | local selection/install implementation is partial; comparison workload needed |
| Binary packages | Remote binhost install | emerge -G | — | isolated ROOT/binhost | URL parsing/download implementation is partial; Packages-index/signature and comparison work remain |
| Install | New package transaction | emerge | — | isolated ROOT | journaled payload/VDB transaction and live evidence exist; broad lifecycle differential gate open |
| Install | Upgrade/reinstall/slot coexistence | emerge | — | isolated ROOT | replacement payload/VDB rollback and live reinstall/upgrade evidence exist; broader preserve-libs corpus open |
| Removal | Uninstall | emerge -C | — | isolated ROOT | explicit-ROOT journaled removal covered; lifecycle differential gate open |
| Removal | Depclean calculation | emerge -pc | — | immutable snapshot | workload needed |
| Removal | Depclean execution | emerge -c | — | isolated ROOT | implementation needed |
| Recovery | Resume after build failure | emerge --resume | — | isolated ROOT | state-bound resume/continuation implementation and tests exist; comparison workload needed |
| Recovery | Crash recovery during merge | Portage safety behavior | — | isolated ROOT | durable recovery implemented; exhaustive kill-boundary matrix open |
| Maintenance | Preserved rebuild scan | emerge @preserved-rebuild | revdep-rebuild | isolated/live read-only | workload needed |
| Maintenance | News/config/env queries | Portage tools | eselect/dispatch-conf | isolated ROOT | workload needed |

Timing cells enter the README only from correctness-gated JSON reports. Tasks
that mutate repositories, roots, VDB, world, configuration, or distfiles must
run in cloned or isolated state.
