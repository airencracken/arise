# Package environment parity audit — 2026-07-23

## Incident

Arise built `llvm-core/llvm-22.1.8` without materializing the effective
`LLVM_TARGETS` aggregate variable from its enabled `llvm_targets_*` USE flags.
LLVM consequently reported an unknown host and registered no targets. Rebuilding
LLVM after repairing that projection fixed LLVM itself, but the separately
installed `llvm-core/clang-22.1.8` artifact remained tainted: its metadata
reported the correct host triple while it still had no registered targets.
`llvm-runtimes/compiler-rt-22.1.8` exposed the latent defect only when CMake
performed a real compiler test.

The repeated repair was caused by treating package metadata, LLVM, Clang, and
the runtime stack as one health boundary. They are separate artifacts and need
separate functional checks.

## Confirmed systemic gaps

### USE_EXPAND aggregate variables

Package execution reconstructed `USE`, but previously did not reconstruct
active USE_EXPAND aggregate variables such as `LLVM_TARGETS`, `ABI_X86`,
`PYTHON_TARGETS`, and `CPU_FLAGS_X86`. Ebuilds and eclasses may consume either
the individual USE flags or the aggregate variable, so a correct `USE` string
alone is insufficient.

The execution environment now derives every active USE_EXPAND aggregate from
the final effective USE set. Command-line overrides for active aggregate
variables are accepted and replace, rather than append to, the corresponding
group flags.

### Build-machine and cross-toolchain variables

The package execution allowlist omitted several artifact-affecting variables,
including `BUILD_*`, `PKG_CONFIG*`, `GCC_SPECS`, `RUSTFLAGS`, `NINJAFLAGS`,
Fortran tools, Objective-C tools, and per-target flag families. Native builds
often concealed this omission by finding reasonable defaults. Cross builds
could select the wrong compiler, flags, or dependency metadata.

These variables are now passed through to package phases.

### Artifact provenance

VDB entries had no marker identifying the execution-environment semantics under
which an Arise artifact was built. New Arise merges record
`ARISE_PHASE_ENV_ABI`. The resolver forces a reinstall when a package carrying
an older Arise marker is encountered. Unmarked packages, including packages
installed by Portage, are deliberately not treated as stale merely because
they lack the Arise marker.

Increment this ABI whenever a change to package-phase environment construction
can alter produced artifacts.

### Preflight scope

Install preflight resolves recipes, policy, dependencies, and planned actions.
It does not configure or compile every package and therefore cannot prove that
an installed compiler or other build tool works. The success message now calls
this a recipe/policy preflight and explicitly says build tools were not
executed.

For bootstrap-sensitive continuations, add narrow functional gates before
starting the expensive transaction. A compiler gate must compile an object;
version strings and target triples are not sufficient. LLVM-family gates should
also verify the expected registered target.

## Installed-state inventory

A read-only VDB scan found 260 installed package entries missing at least one
aggregate variable implied by active USE flags. This is an upper bound on
possible taint, not a rebuild list. Counts by missing group were:

| Group | Installed entries |
| --- | ---: |
| ELIBC | 260 |
| KERNEL | 260 |
| PYTHON_TARGETS | 93 |
| ABI_X86 | 60 |
| PYTHON_SINGLE_TARGET | 20 |
| CPU_FLAGS_X86 | 13 |
| LLVM_TARGETS | 5 |
| LLVM_SLOT | 4 |
| LUA_SINGLE_TARGET | 4 |
| AMDGPU_TARGETS | 1 |
| CURL_SSL | 1 |
| GUILE_SINGLE_TARGET | 1 |

Missing persisted aggregate values do not alone prove a broken artifact.
Packages differ in whether they consume the aggregate, individual USE flags, or
neither during artifact production. The LLVM/Clang failure is confirmed because
the recipes consume `LLVM_TARGETS` directly and the installed tools failed
functional target checks.

Evidence captured during the audit:

- `/tmp/arise-use-expand-taint-scan-v2.txt`
- `/tmp/arise-use-expand-all-taint.txt`
- `/tmp/arise-use-expand-taint-by-time.txt`

These paths are temporary operational evidence and are not repository fixtures.

## Staged remediation

1. Complete the currently running Clang 22 and runtime-stack repair.
2. Verify Clang 22 lists an x86-64 target and can compile, link, and execute a
   trivial native program.
3. Build and test a fresh Arise binary containing the environment fixes.
4. Discard the previously saved 113-package world plan because the installed
   state changed. Generate a new preflight plan with the fresh binary.
5. Use that fresh plan for the world continuation. New VDB entries will carry
   the phase-environment ABI marker.
6. Audit confirmed direct consumers of the affected aggregate variables by
   artifact class. Repair functionally broken artifacts; do not blindly rebuild
   all 260 inventory entries.
7. Repair the independently confirmed LLVM/Clang 19 target-registration defect
   separately; it is not part of the current Clang 22 critical path.

## Regression requirements

- Effective USE must deterministically project to every active USE_EXPAND
  aggregate.
- An explicit aggregate override must replace stale group flags.
- Build and cross-toolchain variables must reach package phases.
- New VDB metadata must record the phase-environment ABI.
- Resolver tests must distinguish old marked Arise artifacts from unmarked
  Portage artifacts.
- Bootstrap continuation scripts must perform functional tool checks before
  authorizing an expensive saved plan.
