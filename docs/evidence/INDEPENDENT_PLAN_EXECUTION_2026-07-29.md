# Independent plan execution evidence — 2026-07-29

This record closes the first independent resolver-to-execution correctness
case. It is portable automated evidence, not a claim that every live Portage
difference is correct.

## Origin

A live `dev-util/pkgcheck` comparison found Arise scheduling eleven Perl
consumer repairs after `dev-lang/perl` moved from subslot 5.40 to 5.42 while
Portage's package-specific pretend operation retained the stale consumers.
The frozen live comparison classified both final states as valid once
pre-existing VDB defects and repair provenance were handled independently.

## Hermetic reduction

The permanent fixture contains only:

- `dev-libs/provider`, upgraded from subslot 1 to subslot 2; and
- `app-misc/consumer`, initially recorded with
  `dev-libs/provider:0/1=`.

The test constructs the stale disposable VDB through real phase-protocol and
journaled merge operations. The resolver must emit exactly one same-version
consumer reinstall. That exact action is frozen, independently validated,
classified against retaining the baseline, executed through the normal
executor, and checked by an independent VDB rescan.

The predicted and observed consumer metadata must both contain
`dev-libs/provider:0/2=`. Missing, unexpected, partial, identity, repository,
subslot, USE and dependency-binding mutations fail the gate.

## Automated gates

- `TestResolverPlanClassificationExecutionAndObservedState`
- `TestPredictedCommittedStateMatchesDisposableRootVDB`
- `TestReplacementIsJustifiedByRepairedRuntimeDependency`
- `TestClassifiedAssessmentComparesPredictedCommittedBindings`
- committed-state mutation and atomicity cases in `internal/planvalidate`

The fixture runs without root privileges when Portage's `sandbox` executable
is available. The full repository test suite includes it by default.

## Live-root completion

After the portable gates passed, the same host state was previewed with:

```console
arise --pretend --verbose --complete-graph update dev-util/pkgcheck
```

The first preview exposed a final safety defect: four version-changing repairs
were labeled as reinstalls. Commit `d129ed6` corrected stale-binding repairs to
use the update replacement lifecycle whenever the selected CPV differs and to
retain reinstall only for exact-version repairs. The corrected preview
contained seven reinstalls, four updates, zero removals and zero conflicts.

All eleven live package transactions then committed successfully. The
post-repair differential reported:

```json
{
  "arise_count": 0,
  "emerge_count": 0,
  "arise_verified": true,
  "portage_resolved": true,
  "comparison_class": "equivalent-valid",
  "accepted": true,
  "equivalent": true
}
```

No action or final-state diagnostics were truncated or omitted. This completes
the chain from live discovery through frozen classification, hermetic
reduction, mutation testing, disposable-root execution, live-root repair and
post-execution convergence.
