# Execution recovery and bounded retry plan

## Objective

Long package transactions must preserve committed work, roll back failed
uncommitted work, continue independent work when requested, and recover from a
changed installed state without replaying successful packages. Recovery must
remain deterministic, bounded, durable, and subject to the same plan approval
contract as the initial execution.

This document distinguishes two modes:

- `--keep-going`: continue useful work after a failure, but do not retry the
  same failed action automatically merely because it failed.
- `--keep-retrying` (working name): perform bounded re-resolution/rebatch cycles
  when state changes or a classified transient failure may make another attempt
  useful.

The final option spelling may change. The safety and termination semantics may
not.

## Common invariants

1. A package is complete only after its payload and VDB transaction commits.
2. A failed pre-commit package is rolled back through its journal and never
   marked complete.
3. A post-commit lifecycle failure records the package as committed and must not
   cause its build or merge to be replayed.
4. Running peers are joined before their state is used for re-resolution.
5. Every re-resolution reads a locked, recovered ROOT/VDB snapshot.
6. Committed actions become installed resolver inputs and disappear from future
   action sets unless the new state independently requires another action.
7. No new action, version, repository, USE configuration, removal, or mutation
   may inherit approval merely because it appeared during recovery.
8. Resume state, failure state, cycle state, and continuation plans are written
   atomically and durably.
9. Cancellation and external interruption terminate recovery promptly.
10. Every mode has explicit progress and cycle limits; no infinite retry loop is
    possible.

## Failure accounting

Persist a structured failure record containing:

- action identity and original plan position;
- attempt and recovery-cycle numbers;
- failure stage: fetch, worker startup, phase, image finalization, pre-commit
  validation, merge, lifecycle, or generated-cache maintenance;
- whether the package committed;
- durable log and journal identifiers;
- normalized error class and fingerprint;
- prerequisite and dependent identities at failure time;
- repository/configuration/VDB state fingerprints;
- retry classification and rationale.

Failure classes should initially be conservative:

- `permanent-action`: deterministic build, QA, policy, or compatibility error;
- `blocked-by-failure`: dependent closure of an unsuccessful prerequisite;
- `transient-resource`: network, mirror, temporary storage, load, or explicitly
  classified resource failure;
- `state-sensitive`: may change after another package commits or the graph is
  recalculated;
- `committed-post-action`: payload committed; maintenance or lifecycle failed;
- `external-cancel`: signal, context cancellation, or operator stop.

Unknown errors default to permanent for the current cycle.

## `--keep-going` execution semantics

When one action fails:

1. Stop releasing its dependent closure.
2. Roll back any uncommitted filesystem/VDB mutation for that action.
3. Preserve its build log, failure record, and failed image according to the
   configured debugging policy.
4. Do not cancel already-running independent peers solely because of the
   package failure. Let them reach a safe terminal boundary.
5. Continue dependency-ready actions whose prerequisite closure contains no
   failed node.
6. After the current executable frontier drains, recover journals and acquire a
   locked state snapshot.
7. Re-run the resolver for the original user goals against actual committed
   state.
8. Remove already satisfied work and recalculate dependency edges and blocked
   closures.

The recalculated result may execute automatically only if it is a verified
subset of the approved plan:

- every action has the same stable action identity;
- version, slot/subslot, repository, USE state, merge type, and mutation kind
  are unchanged;
- no new removal or replacement appears;
- all approved-state inputs still verify;
- only completed, failed, or newly blocked actions were removed and dependency
  ordering was not weakened unsafely.

If any new or changed action appears, Arise writes a separately named
continuation plan, prints the plan/digest difference, retains resume evidence,
and stops for explicit approval. `--keep-going` must never turn a new solution
into an implicitly authorized mutation.

The command exits nonzero if any original goal remains unsatisfied, even if a
large independent subset committed successfully.

## Bounded `--keep-retrying` semantics

The retry mode builds on `--keep-going` and adds recovery cycles. Proposed CLI:

```text
--keep-retrying
--retry-cycles=N
--retry-attempts-per-package=N
```

Defaults should be conservative, such as three recovery cycles and two attempts
per action, and must be documented before enabling mutation.

One recovery cycle is:

1. Execute the currently approved, dependency-ready batch.
2. Drain or safely cancel workers according to dependency independence.
3. Recover journals and persist all commit/failure outcomes.
4. Acquire the operation/VDB lock and snapshot actual state.
5. Re-resolve the original goals.
6. Verify approval/subset eligibility.
7. Reclassify failures and construct the next deterministic batch.
8. Persist the cycle record before starting new work.

An action may be retried only when at least one of these is true:

- its failure is explicitly classified transient;
- its prerequisite or installed-state fingerprint changed;
- a prior conflicting/obsolete package was successfully replaced or removed;
- the recalculated graph changes its valid execution context;
- the operator explicitly requested retry of that exact action.

A deterministic permanent failure with the same action, inputs, state
fingerprint, and error fingerprint is not retried in the next cycle.

## Progress and termination rules

Track a cycle fingerprint over:

- committed package identities;
- remaining approved action identities;
- failed action/error fingerprints;
- blocked frontier;
- relevant VDB/configuration/repository state;
- continuation plan digest.

Stop retrying when any condition holds:

- all goals are satisfied;
- the configured cycle threshold is reached;
- an action reaches its attempt threshold;
- two consecutive completed cycles have identical fingerprints and no commits;
- no dependency-ready approved action remains;
- re-resolution requires a plan that is not an approved subset;
- a permanent policy/safety failure occurs;
- the operator cancels execution.

Backoff is appropriate only for classified transient resource failures. It must
be context-cancellable and capped; it must not delay deterministic build or
policy failures.

## Rebatching

Rebatching is graph work, not list slicing. Each cycle must reconstruct the DAG
from the resolver result and actual installed state. A valid batch contains only
actions whose current prerequisites are satisfied or included earlier in the
same deterministic DAG.

The scheduler may:

- remove actions satisfied by previous commits;
- release branches formerly blocked by state that has genuinely changed;
- repartition independent components;
- adjust ready ordering without changing stable action identity;
- retain prepared images only when their complete input/state fingerprint still
  matches the recalculated action.

It may not reuse a prepared image after repository, USE, dependency-root,
toolchain, eclass, phase-query, or relevant installed-state changes unless the
cache key proves equivalence.

## Resume and evidence files

Resume schema must be versioned and extended with:

- original goal arguments and approved plan digest;
- action completion/commit state;
- failed and blocked states;
- attempt and cycle counters;
- action and error fingerprints;
- last verified state digest;
- continuation plan path/digest when approval is required;
- terminal reason.

Never overwrite the original failed-run evidence when generating a changed
continuation plan. Use a new artifact generation or immutable operation
directory and atomically update only a small current pointer.

## User-facing reporting

At failure and at each cycle boundary, report:

- committed, failed, running, blocked, and remaining counts;
- which independent work will continue;
- which dependent closure was blocked and why;
- whether re-resolution removed already completed work;
- cycle and per-action attempt counters;
- whether the next plan is an approved subset;
- the reason for retrying or refusing each failed action;
- the new plan path and SHA-256 when approval is required;
- an exact final terminal reason.

Do not print `Completed package` for an uncommitted action. Distinguish committed
payload with failed post-commit maintenance from an entirely failed package.

## Test matrix

### Scheduler tests

- one failed branch does not cancel independent branches under `--keep-going`;
- dependents of the failed node never start;
- already-running independent peers can commit;
- commit callbacks and resume records remain ordered and durable;
- recalculated edges release only valid newly ready work;
- output remains deterministic for a fixed completion schedule.

### Re-resolution tests

- committed actions disappear from the continuation result;
- failed uncommitted actions remain eligible or blocked as appropriate;
- post-commit failures never replay committed payload;
- a changed/new action forces a separately approved continuation plan;
- an exact approved subset continues automatically;
- removed prerequisites and changed slots cannot weaken ordering silently.

### Retry tests

- transient failure succeeds on a later bounded attempt;
- unchanged deterministic failure is not retried endlessly;
- state-sensitive failure retries only after its state fingerprint changes;
- no-progress fingerprint stops before the maximum when appropriate;
- cycle and per-package thresholds are enforced independently;
- cancellation interrupts backoff and running work;
- restart from every persisted cycle boundary is deterministic.

### Live/disposable integration

- multi-branch package DAG with injected build failure;
- merge rollback followed by independent commits;
- process death between commit and resume update;
- repository/configuration drift between cycles;
- continuation-plan approval mismatch;
- large partial world plan proving successful packages are not rebuilt.

## Delivery order

1. Extend failure and resume schemas with action/cycle state.
2. Implement independent-subgraph `--keep-going` without re-resolution.
3. Add locked re-resolution and approved-subset verification.
4. Add continuation-plan generation for changed solutions.
5. Add failure classification and no-progress fingerprints.
6. Implement bounded retry cycles behind an experimental flag.
7. Add crash, race, and large synthetic-DAG tests.
8. Promote CLI naming/defaults only after disposable-root evidence passes.
