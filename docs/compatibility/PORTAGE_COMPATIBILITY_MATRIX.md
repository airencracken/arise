# Portage Compatibility Matrix

This is the tracking ledger for Arise's user-visible Portage compatibility.
The CLI inventory is derived from `emerge(1)`, not only `emerge --help`; help
output is a smoke check because it omits options and interaction semantics. The
current reference host uses Portage 3.0.81.2, installed through Arise on
2026-07-24. Reference changes require an explicit matrix review and fixture
update.

Status values are `supported`, `partial`, `planned`, and `not-applicable`.
`Supported` requires a behavior test, not merely successful parsing.

## Resolver CLI (P3)

| Portage interface | Arise spelling/value | Status | Enforcement |
|---|---|---|---|
| `-p`, `--pretend` | same | supported | registration and zero-mutation tests |
| `-u`, `--update` | same | supported | resolver config and live pretend matrix |
| `-D`, `--deep` | same | supported | dependency-depth tests |
| `-N`, `--newuse` | same | supported | USE transition fixtures |
| `--changed-use` | same | supported | reinstall classification tests |
| `--changed-deps` | same | supported | installed/current dependency tests |
| `--dynamic-deps[=y/n]` | boolean `--dynamic-deps` | partial | behavior tested; value syntax pending |
| `--complete-graph[=y/n]` | boolean `--complete-graph` | partial | reverse-graph tests; value syntax pending |
| `--with-bdeps=y/n/auto` | same | supported | dependency-class matrix |
| `--keep-going` | same | supported | bounded conflict/cancellation tests |
| `--backtrack=N` | same | supported | hard-ceiling and deterministic ledger tests |
| `-e`, `--emptytree` | same | partial | canonical spellings and bounded regression covered; equivalent live outcome/timing gate open |
| `-O`, `--nodeps` | same | supported | verification-status tests |
| `-o`, `--onlydeps` | same | supported | root/dependency action tests |
| `--root-deps=VALUE` | same | partial | domain policy tested; full value matrix open |
| `-k`, `--usepkg` | same | partial | local policy implemented; P9 breadth open |
| `-K`, `--usepkgonly` | same | partial | fail-closed local selection implemented |
| `--binpkg-respect-use` | same | partial | local USE matching; remote P9 open |
| `-j`, `--jobs=N` | same | supported | speculative resolver determinism tests |
| `-l`, `--load-average=N` | same | supported | context-aware scheduler throttle and configuration tests |
| `--resolver-timeout=DURATION` | Arise extension | supported | structured context cancellation tests |

The executable registration subset lives in
`cmd/arise/main_test.go:TestP3CanonicalResolverFlagsRegistered`. It is a P3
subset of the man-page inventory, not a claim of full emerge CLI parity.

## Environment

| Variable | Meaning | Status | Enforcement |
|---|---|---|---|
| `ROOT` | target filesystem root | supported | path/config and resolver domain tests |
| `PORTAGE_CONFIGROOT` | configuration root | supported | config loading tests |
| `PORTDIR` | primary repository | supported | CLI default tests |
| `DISTDIR` | distfile cache | supported | CLI/fetch tests |
| `PKGDIR` | binary package directory | supported | binpkg selection tests |
| `PORTAGE_TMPDIR` | work/resume base | partial | paths tested; lifecycle parity is P4 |
| `PORTAGE_BINHOST` | binary hosts | partial | ordered URL parsing and remote download tests; Packages-index/signature breadth remains P9 |
| `FEATURES` | execution/package features | partial | typed P4 policy; precedence open |
| `USE`, `ACCEPT_KEYWORDS`, `ACCEPT_LICENSE` | resolver policy | supported | layered config tests |
| `MAKEOPTS`, `CFLAGS`, `CXXFLAGS` | build environment | partial | P4 differential gate open |
| `PORTAGE_LOG_FILE` | durable package log path | supported subset | reserved identity and package-log tests |
| `PORTAGE_LOG_FILTER_FILE_CMD` | post-build log filter | supported | success/failure boundary tests |
| `PORTAGE_ELOG_CLASSES` | selected elog classes | supported | class selection tests |
| `PORTAGE_ELOG_SYSTEM` | elog sinks | partial | echo/save/save-summary/syslog; mail/custom fail visibly |
| `ARISE_CPU_PROFILE` | Arise diagnostic extension | supported | all-command interrupted profiles |
| `ARISE_GO_TRACE` | Go scheduler, blocking, and syscall trace | supported | all-command normal/interrupted profiles |
| `ARISE_HEAP_PROFILE` | live-heap profile written at termination | supported | all-command normal/interrupted profiles |
| `ARISE_ALLOCS_PROFILE` | cumulative allocation profile written at termination | supported | all-command normal/interrupted profiles |

These four diagnostic extensions are exercised by
`support/perf/profile-p3-matrix.sh`.
The harness probes every external profiler before invoking it: Go runtime
profiles are always collected, `perf` is used only when its operation succeeds,
and the additional `strace -f -c` pass is both capability-gated and opt-in.

## Configuration files and directories

| Path relative to `/etc/portage` | Status | Enforcement/notes |
|---|---|---|
| `make.conf` | supported | assignments, incremental variables, command precedence |
| `repos.conf` | supported | order, masters, priority and overlay identity |
| `package.use` | supported | file/directory order, atoms and USE changes |
| `package.accept_keywords` | supported | ordered candidate visibility |
| `package.license` | supported | ordered package license policy |
| `package.mask` | supported | repository/profile/user stacking and reasons |
| `package.unmask` | supported | stacked unmask restoration |
| `package.env` and `env/` | supported | ordered selection, safe paths and precedence |
| selected profile stack | supported | parent traversal and package policy layers |
| `sets/` | planned | custom set parity open |
| `bashrc`, `package.bashrc` | planned | P4 execution ABI |

Important `make.conf` variables are tracked independently from file parsing:

| Variable | Status | Enforcement/notes |
|---|---|---|
| `CONFIG_PROTECT`, `CONFIG_PROTECT_MASK` | partial | regular-file protection, mask precedence and cfg counters covered; type/symlink differentials open |
| `FEATURES` | partial | typed execution/logging policy; full Portage precedence matrix open |
| `PORTAGE_LOGDIR` | partial | durable ordinary/split/compressed package logs; directory differential open |
| `PKGDIR`, `DISTDIR`, `PORTAGE_TMPDIR` | partial | paths consumed; full lifecycle differential open |

## Installed-state and mutation files

| Interface | Status | Enforcement/notes |
|---|---|---|
| `/var/db/pkg/*/*/CONTENTS` | partial | Portage root-relative obj/dir/sym records; empty packages publish exactly zero bytes; full metadata differential open |
| `environment.bz2` | partial | native deterministic snapshot and decompression tests; old-lifecycle discovery strips stale sandbox/preload execution controls; variable differential open |
| `NEEDED`, `NEEDED.ELF.2` | partial | native ELF dynamic-section generation; multilib corpus differential open |
| `BUILD_TIME`, package/global `COUNTER` | partial | VDB-locked journaled allocation and rollback; Portage differential open |
| installed built slot bindings | partial | new merges expand `:=` to explicit `slot/subslot=` metadata; unexpanded historical `:=` is not treated as a concrete rebuild binding |
| `/var/lib/portage/world` | partial | locked atomic deselect and explicit world maintenance preserve mode/ownership; optional saved-plan approval remains state-bound; broader repository-move and redundant-constraint differential corpus remains open |
| `/var/log/emerge.log` | partial | locked compatibility projection; readers tolerate historical NUL padding and append fails closed on NUL-corrupt logs; wider tool-format differential open |
| Portage VDB lock | supported subset | held across ownership validation, recovery, mutation and commit |

## Maintenance commands

| Portage interface | Arise spelling/value | Status | Enforcement |
|---|---|---|---|
| installed `pkg_config` phase | `arise config ATOM` | supported subset | persisted lifecycle and legacy installed-environment tests; wider EAPI/package differential corpus remains open |
| `dispatch-conf` | `arise dispatch-conf` | partial | recursive/explicit discovery, decisions, safe three-way premerge, atomic confined archives, hooks, mixed-file diffs, metadata, cancellation, ROOT/config-root, schema, property, adversarial and installed-Portage archive differentials; session rollback/recovery and full command differential corpus remain open |
| `emaint --check/--fix world` | `arise maintain world --check/--fix` | partial | deterministic text/JSON checks, configured-repository coverage, direct explicit repair, optional state-bound saved plans, lock-time revalidation, atomic repair, and live unavailable-entry parity; redundant constraints and wider move/mask corpus remain open |

## Maintenance rules

1. Review installed `emerge(1)`, `make.conf(5)`, and Portage configuration
   manuals when the reference version changes.
2. Add a matrix row before advertising compatibility.
3. A registered flag with missing value/interaction semantics remains partial.
4. Live probes compare normalized outcomes; parsing success never closes a row.
5. Unsupported enabled behavior must fail visibly before mutation.
