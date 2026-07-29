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
