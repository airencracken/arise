# Phase state and working-directory audit

The reported `sys-libs/binutils-libs-2.47` failure exposed a general phase-state
problem. Its `src_unpack` changes `S` to `${WORKDIR}/binutils-2.47`. Arise's
separate `src_prepare` worker discarded that value and tried to enter the
initial `${WORKDIR}/binutils-libs-2.47` path.

The earlier launch-directory fix, commit `3840290`, is already an ancestor of
the audited source, `f30c98a`. It selects a usable directory for starting the
worker process. It did not establish persistence of package state between
workers. The earlier installed-lifecycle fix, `ea86492`, is also already merged.

## Scope and reference

The audit covers the Bash worker, environment serialization, the source build
driver, installed and replacement lifecycle hooks, isolation path translation,
and tests that claim to check those contracts.

Reference rules are PMS sections 11.2 and 9.1.1 on
[variable persistence and phase entry directories](https://projects.gentoo.org/pms/8/pms.html),
and the [EAPI 9 specification](https://projects.gentoo.org/pms/9/pms.html).
The installed Portage implementation was also inspected, particularly
`save-ebuild-env.sh`, `phase-functions.sh`, and `ebuild.sh`.

Reference environment: Portage 3.0.82.2, Bash 5.3.20, Go 1.27.3, and Gentoo
repository commit `7daa9f98bb4b46c5ee28f6cb4f7d37ef08d2b3e7`.

A conservative text scan found 234 indented `S=` statements inside function
blocks across 28 package directories. This includes command-scoped assignments
and multiple versions; it is an inventory of candidate patterns, not a count
of proven broken builds. Examples include binutils-libs, liberation-fonts,
and database test helpers. Eclasses can introduce additional cases.

## Findings and changes

| Boundary | Finding | Resulting behavior |
| --- | --- | --- |
| Source directory | Snapshots excluded `S`, and loading reset it to the original request. | Source phases retain their effective `S`, including changes made while unpacking. |
| Package shell state | Re-sourcing the ebuild resurrected unset variables and functions. Snapshots omitted helper functions and readonly values. | Complete snapshots restore package variables, arrays, exported values, helper and phase functions, and explicit unsets of configured values. |
| Serializer scope | Locals such as `name`, `output`, and `phase_name` shadowed package globals. Worker control variables also used ordinary names. | Worker bookkeeping uses its reserved namespace. Bulk declaration output preserves package values without a subprocess per variable. |
| Helper settings | Compression and strip queues were excluded with other worker internals. | The relevant helper queues survive the phase transition; isolation controls remain excluded. |
| Installed state | The VDB and built GPKGs received initial request variables rather than the final package environment. Post-install and removal-hook state was lost. | Build snapshots feed artifacts and the VDB; pre-install, post-install, and successive removal hooks carry state forward. Committed environment updates publish atomically. |
| Lifecycle discovery | Stored metadata and legacy fallback could resurrect a deliberately removed hook or miss a dynamically created hook. | Complete snapshots carry an internal format marker and provide authoritative hook definitions. Legacy installed snapshots retain their compatibility fallback. |
| Hook execution | A static disabled-USE shortcut could skip a hook replaced by an earlier phase. | Execution uses the saved function definition; static assessment no longer suppresses the worker. |
| Runtime paths and policy | Loading an installed environment could restore deleted build paths. Unconfigured lifecycle policy could fall back to a sandbox despite requesting an unsandboxed hook. | Current scratch, home, log, and build paths are restored from the request. Lifecycle overrides explicitly select their policy. |
| Phase directories | EAPI 8 hooks entered a populated `WORKDIR`; unpack manufactured an empty `S`, hiding missing sources. | Hooks receive an empty temporary directory. Source-less default phases use the defined fallback; an explicit source phase with missing sources fails clearly. |
| Source confinement | String-prefix checks missed `..`, symlink escapes, and changes made during a phase. | Effective source paths are checked before and after execution, including replacement of the default source path with an escaping symlink. |
| Isolation translation | Snapshot input/output paths were not translated into the bubblewrap view. Atomic output needs a writable parent directory. | State paths use the corresponding bound directory, or an explicit external input/output binding. |
| Pretend and export state | Shared-shell requests could mix pretend with build state; EAPI 9 phase identifiers were re-exported. | Pretend publishes no snapshot and cannot share a build batch. Phase identifiers retain the EAPI's visibility. |
| Diagnostics and tests | Worker stderr was lost after a terminal event. Numerous test assertions used ordinary nonzero returns which the worker deliberately ignores. | Stderr accompanies process failures. Contract assertions fail explicitly; intentional nonfatal-return tests remain intact. |

## Verification

The new split-versus-batch tests reproduced the reported missing-directory
failure before the changes. They also exposed lost functions, readonly values,
unsets, compression settings, and serializer-name collisions. Tests check
observable files and explicit failures rather than snapshot implementation text.

Live reference tests compare installed fixture contents and variable visibility
with Portage for EAPIs 7, 8, and 9. A separate test runs the actual
binutils-libs 2.47 ebuild through unpack and prepare, using the cached source and
patch archives after checking their Manifest identities.

The visibility comparison covers `EBUILD_PHASE_FUNC`. Portage 3.0.82.2 also
exports `S` during configure/compile for its libtool integration, including
EAPI 9. Arise's EAPI 9 shell-variable export rules are checked separately
against PMS; the reference test does not assert equality of that implementation
detail.

The disposable-root lifecycle test builds and installs a package, removes its
original build/repository inputs, and runs removal hooks from the saved VDB
environment. Atomicity tests retain the previous environment when publication
fails. Adversarial tests cover directory traversal, symlink replacement,
manager-owned runtime paths, failed-phase snapshots, and snapshot bindings.

Validation passed: the complete default test suite, Go vet, the full internal race
suite, static binary build, documentation
checks, and the complexity ratchet. The live Portage comparisons passed all
three EAPIs; the actual binutils unpack/prepare test passed without skipping.

Commands:

```sh
make test
go vet ./...
go test -race ./internal/phaseproto ./internal/rebuild -count=1 -timeout 300s
make test-race
go test -tags=live_portage ./internal/phaseproto \
	-run 'TestLivePhaseStateMatchesPortage|TestLiveBinutilsLibsUnpackAndPrepare' \
	-count=1 -v -timeout 300s
make build
bash support/check-docs.sh
make audit-complexity
```

## Limits

This audit does not establish compatibility for every eclass or complete the
172-package host transaction. The real binutils test covers unpack and prepare,
not its complete compile/install cycle. No package was installed on the host.
Fresh-stage3 upgrade, interruption/resume, linkage, and reboot acceptance remain
separate gates.

The tests run as an unprivileged user. Actual root-to-portage credential changes
and full bubblewrap execution were not exercised; snapshot translation is
covered independently. Shellcheck was unavailable; Bash syntax validation ran.
Previously installed snapshots cannot recover values
that older Arise versions never saved; their legacy fallback remains available.

## Rollout

The execution ABI advances from 4 to 5. State loss could produce a successful
build with different output, so ABI-2/3/4 Arise artifacts require rebuilding.
Resolver tests cover that repair selection, retention of current-ABI packages,
and retention of unmarked Portage packages. This can add rebuilds to a future
transaction plan. No host plan was executed as part of the audit.
