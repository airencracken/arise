# Portage Phase-Edge Parity Audit — 2026-07-22

## Scope

This follow-up audits boundaries omitted by the 2026-07-21 execution review:
fetch denial, miscellaneous hook ordering, cleanup, real-root credentials, and
strict install-QA behavior. The executable reference is the Portage
installation on the audit host (Python 3.13), principally `doebuild.py`,
`_spawn_nofetch.py`, `EbuildPhase.py`, and `vartree.py`.

No live mutation is justified by this audit. These findings refine the gates
that must pass before the normal effective FEATURES set replaces the reduced
dogfood configuration.

## Newly confirmed blockers

### P0 — the phase process contract has no jobserver transport

Arise propagates configured `MAKEOPTS`, but its Go worker launch has no model
for inherited jobserver file descriptors or FIFO authentication. A textual
`MAKEFLAGS=--jobserver-auth=R,W` is not sufficient: the descriptors must remain
open across namespace creation, `runuser`, sandbox, and Bash, and a FIFO-backed
jobserver must remain accessible to the portage user's complete supplementary
group set.

The real-root gate therefore has two independent parts:

1. the worker must have the Portage uid, primary gid, and exact supplementary
   groups expected by the installed account; and
2. a nested make must acquire and return a real jobserver token after the full
   launcher chain, with no `jobserver unavailable` fallback.

Passing `id` alone is not evidence for the second part. The test must inspect
the child process and exercise token contention with at least two workers.

### P0 — cleanup is protocol behavior, not temporary-directory deletion

Portage executes `die_hooks` after a failed ebuild phase. With `fail-clean` and
without `noclean`, it then executes the ebuild `clean` command. After merge it
executes `success_hooks` or `die_hooks`, and normally executes `clean` only when
postinst did not fail. Standalone unmerge executes `cleanrm` after removal
lifecycle processing and applies separate unmerge-log retention rules.

Arise currently removes its Go-created work directory according to
`fail-clean`, but it does not execute these Bash commands. Directory removal is
not equivalent: the commands run Portage shell cleanup functions, hooks, elog
processing, and phase-specific environment setup. They need explicit protocol
commands and an ordering matrix covering build failure, merge failure,
postinst failure, successful merge, prerm/postrm failure, and interruption.

Hook failure must not replace the causal package result unless Portage does so.
The durable log and resume record must retain both statuses.

### P0 — `RESTRICT=fetch` is a fetch-result path, not a policy rejection

Portage invokes `pkg_nofetch` when either effective `RESTRICT` contains
`fetch` or the ebuild explicitly overrides the default `pkg_nofetch`, even
without `RESTRICT=fetch`. It uses a private temporary build directory and skips
the call during parallel-fetch mode. The hook can emit package-specific manual
download instructions; acquisition still remains unsuccessful until every
Manifest artifact is present and verified.

Arise previously rejected `RESTRICT=fetch/pkg_nofetch` in package preflight.
The initial implementation now verifies cached restricted artifacts, emits a
typed manual-fetch result for missing/corrupt entries, and runs direct ebuild
overrides through the phase worker. Remaining coverage:

- parallel fetch does not duplicate hook output; and
- inherited/eclass-exported overrides are discovered; and
- hook failure remains secondary to the unavailable verified artifact in the
  durable package summary.

### P0 — strict QA needs outcome differentials, not discovery tests

Discovering and executing Portage's versioned install-QA scripts proves only
that the scripts are reachable. The current Arise policy accepts `strict`,
`multilib-strict`, and `qa-unresolved-soname-deps`, but no differential proves
which warnings become fatal, at what phase boundary, or whether a failure
occurs before payload/VDB mutation.

Create controlled images with one defect per check plus a clean control. Run
each under the normal effective FEATURES and with the relevant feature
disabled. Compare Portage and Arise status, QA class/text normalization,
mutation boundary, retained build directory, hook selection, and resume state.
The representative matrix must report executed/skipped/failed/not-modeled for
every check; discovery cannot count as execution evidence.

## Ordering contract to encode

The next tests should use this state-machine shape rather than independent
"phase ran" assertions:

```text
verified fetch
  -> pkg_setup -> source phases -> pkg_preinst -> image QA
  -> payload/VDB transaction -> pkg_postinst
  -> success_hooks | die_hooks -> clean (conditional)

standalone removal
  -> pkg_prerm -> payload/VDB transaction -> pkg_postrm
  -> cleanrm -> env/linker maintenance
```

Arise deliberately needs a stronger transaction boundary than Portage for
arbitrary lifecycle writes. Consequently, matching phase order is necessary
but not sufficient: each pre-commit write must have a journal preimage, and
each post-commit failure must be represented as committed maintenance work
rather than causing a rebuild of an already installed package.

## Recommended implementation order

1. Add synthetic differential fixtures for `pkg_nofetch`, hook/clean ordering,
   strict QA, and credential/jobserver identity before implementation.
2. Implement `pkg_nofetch`, since it is read-mostly, already exists in the
   worker ABI, and closes a fetch-plan diagnostic gap without authorizing ROOT
   mutation.
3. Add explicit miscellaneous-function protocol commands for success, die,
   clean, and cleanrm, including causal-status preservation.
4. Implement and test real jobserver transport through the complete launcher
   chain; then run the root credential and supplementary-group matrix.
5. Implement lifecycle write capture and only then admit arbitrary custom
   lifecycle hooks on live ROOT.
6. Run the strict-QA and representative Portage matrix with the unmodified
   effective FEATURES set before another large live `@world` transaction.
