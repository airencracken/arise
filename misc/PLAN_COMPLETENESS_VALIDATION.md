# Plan-completeness validation record

This document records a promising result that is **not yet a verified product
claim**. It exists to keep the observation, caveats, and required validation
work together as the repository and laptop state change.

## Hypothesis

On a difficult, conflicted `@world` update, Arise may complete more of the
selected dependency graph than Portage. The working hypothesis is that Arise's
closure tracking and transactional traversal produce four valid actions that
Portage does not include in its partial conflicted plan.

An equally important null hypothesis remains: one or more of the four actions
is unnecessary or invalid because Arise differs from Portage in set expansion,
dependency-class handling, visibility, USE evaluation, or conflict semantics.

## Observation — 2026-07-16

Development machine state:

- profile: `default/linux/amd64/23.0/split-usr/desktop`
- architecture and accepted keywords: `amd64`
- `PYTHON_TARGETS`: `python3_14`
- running Portage: `3.0.77`, Python `3.13.13`
- Go: `go1.26.3-X:nodwarf5 linux/amd64`
- Arise source checkpoint at the start of the campaign: `6c4aff7`
- machine intentionally has stale, masked, removed, multi-slot, and
  interpreter-transition package state

Comparison command, using the development binaries:

```console
XDG_CACHE_HOME=/tmp/arise-cache arise-plan-compare \
  -arise /tmp/arise-structured \
  -target @world \
  -complete-graph=true \
  -backtrack=BACKTRACK
```

The comparator runs equivalent pretend operations with color disabled and
verbose Portage output, then normalizes CPV, slot/subslot, repository and action.

| Backtrack limit | Arise actions | Portage actions | Arise decisions | Difference |
|---:|---:|---:|---:|---:|
| 20 | 139 | 135 | 1 | 4 only in Arise |
| 100 | 139 | 135 | 1 | 4 only in Arise |
| 1,000 | 139 | 135 | 1 | 4 only in Arise |

The four stable differences at every tested limit were:

- `dev-util/github-cli-2.93.0:0`
- `x11-base/xorg-server-21.1.24:0`
- `x11-drivers/xf86-video-amdgpu-25.0.0-r1:0`
- `x11-drivers/xf86-video-ati-22.0.0:0`

All four CPs are explicit entries in `/var/lib/portage/world`. The selected
versions are visible stable candidates. Shared actions had no remaining CPV or
action-classification mismatch. Raising Portage's backtrack budget did not
change its plan.

## Bugs removed before recording the observation

The initial comparison was not trustworthy. It exposed and led to fixes for:

- omitted `:0` and non-verbose Portage output being treated as differences;
- package-wide rather than per-slot installed replacement classification;
- same-version rebuilds being labeled as new installs;
- complete-graph traversal rebuilding unrelated installed orphans;
- slot-operator rebuilds selecting raw highest, including invisible `9999`,
  versions;
- any-of/provider satisfaction checking only the numerically highest installed
  slot and therefore installing an unnecessary Rust slot;
- tree output traversing the repository graph rather than the selected plan.

The reduction was 320 textual differences, 90 normalized real differences, 47
after action/slot correction, 5 after selected-closure scoping, and 4 after
constraint-aware installed-slot matching.

## Why this is not proof yet

- Portage reports slot conflicts, so its displayed actions may be a partial
  diagnostic graph rather than its maximal solvable prefix.
- The repository, VDB, world file, profile, and user configuration were live,
  not frozen into a reproducible snapshot for these runs.
- Equivalent command-line spelling does not yet prove equivalent defaults for
  every dependency class, especially `BDEPEND` and `IDEPEND`.
- Arise's package-specific USE, keyword, license, provider, and set semantics
  are still under parity work.
- The comparator now consumes Arise's versioned JSON plan, including effective
  USE state. Emerge's displayed USE_EXPAND groups are normalized to canonical
  flag names, and every Portage-reported flag is now an equivalence gate. Hidden
  profile flags reported only by Arise are retained but do not create false
  mismatches.
- A pretend action can be locally valid while the complete transaction remains
  invalid, incorrectly ordered, or impossible to execute.
- More output is not inherently better. Extra actions count as an improvement
  only if they are required, visible, executable, and leave the intended final
  state.

## Related Portage reports checked — 2026-07-16

- Gentoo bug 972632 is a confirmed, unresolved Portage/Python transition bug:
  packages rebuilt for Python 3.14 can be ordered before runtime dependencies
  have gained the same target. Several duplicates were attached through June
  2026. This is relevant to final execution order, but it does not prove the
  present Mesa resolution difference.
- Gentoo bug 923327 is a confirmed, unresolved report of DEPEND being scheduled
  after the package that needs it. It supports keeping an independent ordering
  validator, but its public summary alone is not enough to identify our case.
- Portage 3.0.77 source sets source-install BDEPEND handling to `auto` unless
  explicitly overridden. Arise incorrectly defaulted to `n`; that is now fixed.
- A focused Mesa experiment initially looked superior because Arise emitted a
  smaller conflict-free plan. Inspection showed Arise was not following the
  runtime closure of an already-installed build dependency. That result is
  rejected as evidence of better solving and preserved as a regression shape.

Follow-up narrowed the Mesa result more precisely. Portage 3.0.77 accepts the
exact four CPVs selected by Arise as one transaction at backtrack 100 (one
backtrack used): LLVM 22, llvmgold 22, llvm-toolchain-symlinks 22 and Mesa
26.0.8. It reports the broken Python `packaging` update separately as skipped.
Therefore the four-action transaction itself is supported by Portage's resolver;
the remaining difference is that `emerge --update media-libs/mesa` abandons the
target when its optional update search reaches that unrelated broken transition,
while the pinned request and ordinary package request retain the valid target
plan. This is stronger evidence for useful Arise behavior, but execution in a
disposable ROOT is still required before calling it superior.

## Required validation before making the claim

- [ ] Freeze repository, VDB, profile, world, user configuration, environment,
  and Arise index into one immutable fixture with digests.
- [ ] Record exact Arise, Portage, Python and Go versions in the fixture report.
- [~] Add structured Arise plan output and compare effective USE, dependency
  class, selected reason, slot/subslot and repository for every action. The
  versioned plan, USE, identity and action comparison are active; dependency
  class and structured reason comparison remain.
- [ ] Independently prove why each of the four actions belongs in the selected
  closure, including the exact parent edge and dependency class.
- [ ] Confirm that removing each action makes the proposed final graph invalid,
  or classify it as an optional update rather than a correctness requirement.
- [ ] Validate all atoms in the final hypothetical installed state, not only
  the actions printed by either tool.
- [ ] Repeat on the same frozen broken snapshot at backtrack 20, 100 and 1,000.
- [ ] Repeat on a clean, fully updated snapshot where Portage can produce a
  conflict-free complete plan.
- [ ] Repeat `@system`, selected package, deep/non-deep and each `--with-bdeps`
  mode independently.
- [ ] Execute the validated plan in a disposable root/container and compare the
  final VDB, preserved libraries, blockers, filesystem image and depclean set.
- [ ] Deliberately inject one unnecessary and one missing action to prove the
  validator rejects both false “more complete” and false “equivalent” plans.
- [ ] Preserve this broken-state fixture as a permanent regression and
  performance workload if redistribution of its metadata is acceptable.

## Claim threshold

Only describe Arise as solving a graph that Portage cannot solve when the frozen
fixture proves that:

1. the requested semantics and inputs are equivalent;
2. Arise produces a conflict-free, internally valid and executable plan;
3. Portage fails to do so at the documented backtrack limits;
4. executing Arise's plan produces the validated final state; and
5. independent mutation tests demonstrate that the additional actions are
   required rather than incidental.

Until then, the accurate wording is: **on this live conflicted machine, Arise
produced a stable 139-action plan while Portage displayed 135 actions; four
visible world entries appeared only in Arise at backtrack limits 20, 100 and
1,000. This is a promising plan-completeness result under validation.**
