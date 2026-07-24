# Portage Execution Parity Audit — 2026-07-21

## Decision

Another large live `@world` mutation is **not yet an acceptable parity gate**.
The dogfood runs proved that payload/VDB transactions can make durable progress,
but they also exposed phase-policy, lifecycle, QA, and post-merge maintenance
differences that the existing tests did not exercise.

The executable reference for this audit is the installed Portage implementation,
especially:

- `portage/package/ebuild/doebuild.py` (`_unsandboxed_phases`, `_ipc_phases`,
  `_global_pid_phases`, `_doebuild_spawn`);
- `portage/dbapi/vartree.py` (`dblink.treewalk`, replacement unmerge,
  `pkg_preinst`, `pkg_postinst`, and `env_update`);
- the installed `install-qa-check.d` scripts.

## Findings fixed during this audit

| Area | Finding | Resolution |
|---|---|---|
| namespace launcher | Unsandboxed phases became `bash /usr/bin/unshare`, so Bash tried to parse the ELF executable | Namespace isolation is now always the outer launcher: `unshare ... -- [sandbox] bash ...`; both forms have exact tests |
| installed lifecycle policy | Arise disabled `sandbox` but retained build-phase network, IPC, and PID namespaces | `pkg_preinst`, `pkg_postinst`, `pkg_prerm`, and `pkg_postrm` now mirror Portage: unsandboxed, host network/IPC, global PID namespace, mount namespace retained |
| `pkg_preinst` | It shared the build worker and therefore inherited build isolation | It is now a distinct pre-payload phase after `src_install` |
| old removal hooks | Failed old `pkg_prerm`/`pkg_postrm` made the newly committed replacement look failed | They are reported as warnings while replacement removal continues, matching Portage's autoclean behavior |
| install QA discovery | Arise searched only the obsolete unversioned Portage QA directory | Versioned `/usr/lib/portage/python*/install-qa-check.d` directories are discovered and a live test requires core systemd/udev/library/multilib checks |
| Info index | A generated index error failed an already committed package | All manuals are processed; counts/errors are notices and warnings, as in emerge |
| post-merge environment | Arise omitted Portage's `env_update` and linker-cache maintenance | env.d aggregation, four generated environment files, `ld.so.conf`, and root-aware `ldconfig -X` now have disposable-root differential coverage |
| phase process model | `pkg_setup` shared one worker and one isolation policy with all build phases | Every phase now has a fresh worker plus an explicit saved-environment handoff |
| configured image QA | Core Portage install-QA checks were not discovered or executable | Versioned QA discovery and the minimum Portage QA shell runtime are covered by synthetic and live discovery tests |
| `fixlafiles` | Enabled configuration was silently suppressed by dogfood wrappers | Portage-compatible `.la` rewriting runs before image validation/merge |
| `userpriv` / `usersandbox` | Both flags were conflated and globally rejected | They are distinct and phase-scoped; namespace creation remains privileged, then build phases enter through `runuser -u portage` with usersandbox-controlled sandboxing |
| `pkg_pretend` | Discovered but never executed | Runs during preflight before build/mutation, with elog and failure diagnostics preserved |
| lifecycle eligibility | Custom lifecycle hooks were broadly allowed based on comments/heuristics | Live-root preflight now rejects active custom hooks until their writes are transactionally recoverable |

## Critical blockers

### Closed P0 — post-merge environment and linker maintenance

Portage-compatible environment aggregation and root-aware linker maintenance
are now implemented and differentially tested. The representative library
upgrade canary remains part of the final matrix rather than an implementation
blocker.

Required gate:

- disposable-root comparison of generated environment files and `ld.so.conf`;
- live-root linker-cache update with Portage-compatible root/LDPATH semantics;
- library upgrade proving a newly installed SONAME is immediately resolvable;
- failure is reported as post-commit maintenance without repeating the package.

### P0 — configured FEATURES must no longer be suppressed by test wrappers

The machine's effective Portage configuration enables `fixlafiles`,
`multilib-strict`, `qa-unresolved-soname-deps`, `strict`, `userpriv`, and
`usersandbox`. The `/tmp` dogfood wrappers explicitly disable all six. Therefore
those runs do not establish parity with a normal `emerge` invocation.

The six previously suppressed features are now modeled rather than silently
accepted. Before removing the wrapper override, the real-root credential and QA
matrix must still pass with the machine's effective FEATURES unchanged.

Remaining gate:

- implement or explicitly reject each enabled behavior;
- stop overriding FEATURES in the production dogfood scripts;
- bind the effective FEATURES set into the approved plan and parity report.

### P0 — `pkg_setup` mutation model

`pkg_setup` now runs in its own Portage-policy worker and passes declared state
to later phases through an explicit environment snapshot. An exact unsandboxed
setup can still mutate ROOT before the package journal exists, so custom live
hooks are rejected pending lifecycle transaction support.

Required gate:

- explicit pre-payload lifecycle mutation policy and recovery semantics;
- capability, account, device, and resource-check fixtures.

### P0 — lifecycle writes are outside payload journaling

`pkg_preinst` runs before payload installation and `pkg_postinst` after commit,
as Portage expects, but arbitrary lifecycle writes are not part of Arise's
payload preimage journal. The boundary must be stated and tested; it cannot be
described as whole-package rollback until those writes are captured or isolated.

## Major incomplete parity

- `pkg_pretend` executes during package preflight; resolver-wide ordering still
  needs differential validation.
- `pkg_nofetch` is rejected instead of executed.
- Portage `success_hooks`, `die_hooks`, `clean`, and `cleanrm` are not modeled.
- `userpriv`/`usersandbox` are implemented but still require the real-root
  credential/supplementary-group matrix before the wrapper override is removed.
- `PROPERTIES=live`, `test_network`, `virtual`, and `set` are rejected rather
  than receiving Portage's phase-specific policy.
- `fixlafiles` image rewriting is implemented and unit tested; corpus parity
  remains part of the configured-FEATURES gate.
- install-QA scripts were previously absent; their failure/strict semantics
  still need differential validation now that discovery is fixed.
- build phases share one namespace policy, while Portage varies networking and
  PID/IPC behavior by phase.
- XDG/icon/MIME cache lifecycle remains a rejection gate rather than compatible
  post-merge maintenance.

## Why prior tests passed

The `live_portage` rebuild corpus is predominantly opt-in. An uncached verbose
run skipped ten package builds because their `ARISE_LIVE_*` inputs were unset.
One always-on LLVM preflight failed on enabled `fixlafiles`; a Python cluster
test referenced VDB versions no longer installed. Ordinary unit tests exercised
synthetic ebuilds and Arise's own expected policy rather than comparing the
policy to Portage's phase table.

Every parity report must henceforth classify tests as **executed**, **skipped**,
**failed**, or **not modeled**. A skipped package is not evidence of parity.

## Mandatory representative matrix

| Behavior | Representative package/fixture | Required evidence |
|---|---|---|
| virtual replacement | `virtual/libudev` | old/new hook order, namespace policy, committed-state result |
| file capabilities | `sys-process/htop` | `fcaps` succeeds in `pkg_postinst` |
| systemd/OpenRC units | `sys-process/cronie` | QA, merged-/usr ownership, service lifecycle |
| generated build metadata | `dev-build/autoconf`, `dev-build/cmake` | shared generated files and replacement ownership |
| Info manuals | `app-shells/bash`, `dev-libs/gmp` | package succeeds; index result summarized separately |
| preserved libraries | a controlled provider/consumer pair | registry, consumer report, linker cache, preserved rebuild |
| Go/eclass environment | `dev-lang/go`, `dev-go/gopls` | phase query preflight and shell argument fidelity |
| kernel source tree | `sys-kernel/gentoo-sources` | large-tree merge durability and `RESTRICT=strip` |
| firmware tree | `sys-kernel/linux-firmware` | space preflight, large contents merge, QA |
| CONFIG_PROTECT | controlled `/etc` fixture | pending file ownership and recursive summary |
| account package | `acct-user/root` fixture | stdin/argument fidelity and account lifecycle |
| XDG caches | controlled desktop/icon fixture | post-merge cache behavior without live-root rejection |

## Release gate

A large live world run may resume only when:

1. all P0 items above are implemented or the plan rejects affected packages
   before build/mutation;
2. the parity runner reports zero unexpected skips;
3. the effective FEATURES match the normal Portage invocation;
4. the representative matrix passes in disposable roots;
5. a small live canary passes lifecycle, env-update/linker, QA, interruption,
   and resume checks before expanding the batch.
