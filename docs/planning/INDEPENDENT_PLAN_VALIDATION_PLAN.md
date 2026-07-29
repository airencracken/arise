# Independent plan validation and differential testing plan

## Purpose

Arise must be free to produce a valid package graph that differs from, or is
more complete than, the graph displayed by Portage. Therefore `emerge` output
is valuable comparative evidence, but it is not the correctness oracle.

This plan defines a testing architecture which:

- independently validates the final state produced by a plan;
- explains externally meaningful candidate selection and rejection decisions;
- classifies Arise-versus-Portage differences without requiring action-for-action
  equality;
- preserves difficult live failures as immutable regression fixtures; and
- proves the validator and explanation gates with mutation and adversarial
  tests.

It extends the claim boundaries in
[`PLAN_COMPLETENESS_VALIDATION.md`](../evidence/PLAN_COMPLETENESS_VALIDATION.md)
and the execution lanes in [`TEST_LANES.md`](../testing/TEST_LANES.md).

## Non-goals

- Treating Portage's preferred plan or search order as canonical.
- Reimplementing the resolver inside the validator.
- Recording every internal backtracking or traversal step.
- Making live host state an ordinary hermetic CI dependency.
- Declaring a larger plan better solely because it contains more actions.

## Correctness model

The central contract is:

> Applying an Arise plan to the captured installed state must produce a final
> package state which satisfies the requested operation and all applicable
> package, dependency, repository and policy constraints.

The validator must check at least:

1. active `DEPEND`, `RDEPEND`, `BDEPEND`, `IDEPEND` and `PDEPEND` atoms;
2. version, slot, subslot, slot-operator and repository qualifiers;
3. USE conditionals and USE dependency defaults;
4. blockers, mutually exclusive slots and provider selection;
5. `REQUIRED_USE`;
6. keyword, mask, license and EAPI policy;
7. metadata authority, rejecting execution decisions based only on incomplete
   static metadata;
8. replacement and removal consistency;
9. transaction ordering constraints; and
10. the requested target/set semantics.

The validator must consume a frozen fixture, a plan and explicit policy. It
must not call resolver candidate-selection or backtracking helpers. Shared
parsers and canonical atom data structures are acceptable; shared selection
logic is not.

## Architecture

```text
frozen repository, VDB, profile, world and configuration fixture
                              |
              +---------------+---------------+
              |                               |
       Arise resolver                  Portage reference run
       plan + decisions                plan or failure record
              |                               |
              +---------------+---------------+
                              |
                   independent validator
                              |
             validity results + difference class
```

### Fixture

A portable fixture must identify:

- available versions from every repository;
- metadata source and authority level;
- installed VDB records and build-time USE state;
- repository order and master relationships;
- profile and user policy;
- world and system sets;
- requested operation and flags;
- tool and schema versions; and
- digests for every captured input.

Fixtures should be minimal reductions of real failures where practical. A
larger full-state fixture may remain as evidence and a performance workload,
but each correctness regression should have the smallest faithful fixture that
retains the behavior.

### Plan application

Plan application is a pure transformation:

```go
func ApplyPlan(installed State, plan Plan) (State, []ApplicationViolation)
```

It must reject duplicate or contradictory actions, missing replacement
identities, invalid removal targets and ambiguous repository identities before
semantic validation begins.

### Independent validator

The initial interface should be equivalent to:

```go
type ValidationResult struct {
    Valid      bool
    Violations []Violation
}

type Violation struct {
    Kind        string
    Package     string
    Requirement string
    RequiredBy  string
    Message     string
}

func ValidateFinalState(fixture Fixture, plan Plan) ValidationResult
```

Violation kinds and their JSON representation must be stable enough for golden
fixtures and mutation assertions. Human rendering remains separate.

### Decision ledger

Arise's structured plan should expose bounded, externally meaningful decisions:

- candidate selected;
- installed candidate retained;
- candidate rejected by an atom, USE requirement or policy;
- provider or alternative selected;
- action retracted after a committed replacement; and
- update skipped with the requiring parent and exact constraint.

Each record should include package identity, repository, metadata authority,
outcome, reason kind, relevant atom and requiring parent where applicable.

The ledger must not contain raw recursive traversal paths. Repeated equivalent
reasons must be deduplicated, deterministically ordered and subject to explicit
record and byte limits. Truncation must be represented structurally.

## Differential classification

After validating both available results, comparison should produce one of:

| Classification | Meaning |
|---|---|
| `both-valid-equivalent` | Both plans reach equivalent requested final states. |
| `both-valid-different` | Both final states are valid but differ by permitted policy or optional choices. |
| `arise-valid-portage-fails` | Arise has an independently valid plan and Portage reports failure or no complete plan. |
| `portage-valid-arise-fails` | Portage provides a plan accepted by the independent validator while Arise does not. |
| `arise-invalid` | Arise's final state violates the independent contract. |
| `portage-invalid` | The normalized Portage plan fails the same validator. |
| `comparison-inconclusive` | Inputs or semantics are not equivalent or the reference plan is incomplete. |

Action equality is useful diagnostic detail but is not an acceptance criterion.
Final-state equivalence must account for slots, repositories, USE state,
providers and whether a difference is required or optional.

The gate must fail when:

- Arise produces an invalid final state;
- Portage produces a validator-accepted solution where Arise claims none;
- Arise silently omits a visible candidate or policy rejection from its
  explanation contract;
- diagnostics exceed their bounds; or
- a difference cannot be classified because required provenance is missing.

An independently valid Arise plan must not fail merely because Portage chose
different packages or could not find a plan.

## Initial regression corpus

### Uncached overlay and constrained update

Capture the 2026-07-28 regression shape:

- installed `gui-libs/display-manager-init-1.1.2-r3::gentoo`;
- available `gui-libs/display-manager-init-1.1.2-r4::xlibre`;
- no overlay repository `metadata/md5-cache`;
- authoritative evaluated metadata in the Portage cache;
- installed Sphinx requiring
  `<dev-python/docutils-0.23[python_targets_python3_14(-)]`; and
- visible `dev-python/docutils-0.23::gentoo`.

The assertions are:

- the xlibre candidate is available to resolution;
- any rejection has a structured reason;
- the final graph is independently valid;
- the docutils update is rejected with the exact Sphinx constraint;
- no circular traversal paths enter warnings or the decision ledger; and
- diagnostics remain within their byte and record limits.

The test must not assert an exact total action count.

### Arise-valid, Portage-incomplete case

Reduce the current plan-completeness observation to a fixture where Portage
abandons or reports a partial graph while Arise retains a valid requested
transaction. The validator must accept Arise independently. The fixture remains
evidence, not a superiority claim, until disposable-root execution also passes.

### Adversarial cases

Add generated and hand-written fixtures covering:

- cyclic graphs with equivalent rotations and traversal orders;
- multiple valid providers;
- parallel slots and repository-qualified duplicates;
- forced/masked USE flags;
- stale installed slot-operator bindings;
- incomplete and stale metadata sources;
- unsatisfied blockers and removal ordering;
- an unnecessary action;
- a missing required action; and
- a plausible but invalid higher-version update.

## Test layers

### Unit and property tests

- Validate every violation kind independently.
- Generate valid graphs, remove one required action and require rejection.
- Inject one unnecessary or contradictory action and require classification.
- Permute repository, dependency and map iteration order and require identical
  validation and decision output.
- Generate cyclic graphs and require bounded diagnostics independent of cycle
  count and rotation.

### Schema and contract tests

- Version fixture, validation-result and decision-ledger schemas.
- Reject unknown required fields and invalid identities.
- Round-trip every supported record.
- Enforce deterministic ordering and diagnostic byte limits.

### Mutation tests

Mutations must be killed when they:

- skip one dependency class;
- accept incomplete metadata for execution;
- ignore repository qualification;
- disable USE-dependency validation;
- omit a skipped-candidate explanation;
- restore raw cycle diagnostics;
- accept a missing required action; or
- treat action-count equality as validity.

### Hermetic differential tests

Run Arise and stored normalized Portage outcomes against immutable fixtures.
Validate both independently and assert only the expected classification and
required explanations.

### Live Portage tests

The opt-in live lane captures new differences and checks that the comparator
can classify them. Unreviewed live differences produce bounded artifacts for
fixture reduction; they do not automatically become hermetic golden results.

### Disposable-root tests

Selected plans, especially `arise-valid-portage-fails`, must execute in an
isolated root. Compare final VDB state, preserved libraries, blockers,
filesystem ownership and the depclean closure with the validator's prediction.

## Delivery stages

Implementation began with a deliberately narrow Stage 1/2 vertical slice.
`internal/planvalidate` now defines strict version-1 fixture and plan decoders,
pure action application, deterministic structured violations, frozen available
metadata checks, request-target checks, and independent validation of CPV,
repository, slot, version, blocker, `RDEPEND` and `PDEPEND` semantics.
Unsupported dependency classes, USE dependencies, slot operators and expression
forms originally failed closed. Stage 3 now validates every dependency class
against explicit ROOT/SYSROOT/BROOT state, USE dependencies and defaults,
conditionals, `REQUIRED_USE`, slot operators, masks, keywords, licenses and
supported EAPIs. Exact violations are deterministically deduplicated and
bounded to 256 records with a structured omitted count. The uncached-overlay
and constrained-Docutils regression is captured with evaluated-versus-md5-cache
metadata authority. Unit, integration, property, schema, API-contract,
atomicity, adversarial-input and source-mutation tests cover this boundary.

A translation-only adapter freezes resolver graphs and plans without calling
selection helpers. Verified full plans run impact validation after resolution
and replay it under the operation lock. Both checkpoints now fail closed before
package-state mutation. Identical inherited VDB defects are classified as
pre-existing, but request and action violations remain non-waivable. Set
expansion is frozen as explicit fixture data. `--nodeps` and `--onlydeps` now
carry explicit partial-plan modes: `--nodeps` validates selected targets,
metadata, policy, `REQUIRED_USE`, action ordering and decisions without claiming
dependency closure; `--onlydeps` validates the dependency-only final state,
metadata, policy, ordering and decisions without requiring the omitted target.

### Stage 1: contracts and regression capture

- [ ] Define versioned fixture, validation and decision-ledger schemas.
- [x] Capture and minimize the uncached-overlay/docutils regression.
- [x] Add metadata-authority and bounded-diagnostic contract tests.
- [ ] Document which parser components may be shared with the resolver.

Exit gate: the regression fixture is hermetic and fails when evaluated metadata
is ignored, skipped-update explanation is removed, or cycle paths leak.

### Stage 2: pure plan application and core validation

- [x] Implement pure plan application.
- [x] Validate identity, slots, version atoms, repository qualifiers, blockers
  and runtime dependencies.
- [x] Add property tests for missing, duplicate and contradictory actions.
- [x] Add deterministic structured violations.

Exit gate: deliberately missing and contradictory action mutations are rejected.

### Stage 3: full Gentoo semantics

- [x] Add all dependency classes and explicit root-domain rules.
- [x] Add USE dependencies, conditionals and `REQUIRED_USE`.
- [x] Add mask, keyword, license, EAPI and metadata-authority policy, including
  package-specific keyword/license changes, license groups and mask provenance.
- [x] Validate slot operators, provider justification and transaction order.
  Stable action identities and prerequisite edges prove dependency ordering and
  reject disconnected new provider actions.

Exit gate: all validator-semantic mutations are killed and the frozen corpus
has no unexplained validity result.

### Stage 4: bounded decision ledger

- [x] Define candidate and decision records.
- [x] Emit selected, retained, rejected and skipped outcomes for the committed
  dependency closure.
- [x] Deduplicate and deterministically order records.
- [x] Enforce count and byte limits with structured truncation.
- [x] Add JSON and bounded human rendering without coupling rendering to the
  validation schema.

Exit gate: every visible candidate omitted from a plan has an allowed structured
outcome, and adversarial graphs cannot cause unbounded output.

### Stage 5: classified differential harness

- [x] Validate normalized Arise and Portage final states.
- [x] Implement the classification table.
- [x] Distinguish required, optional and policy-equivalent differences.
- [x] Replace raw action-count parity gates with validity and explanation gates.
- [x] Preserve action-level differences as diagnostics.

The comparator accepts versioned `--arise-state` and `--portage-state`
documents containing frozen validation fixtures and plans plus an optional
`--classification-policy`. It executes independent validation itself; input
cannot assert validity with a boolean. Differing actions without final-state
evidence are inconclusive. Identical verified transactions retain a safe fast
path because they apply the same mutation to the same starting state. Valid
final-state divergence is accepted while exact equivalence remains separately
reported.

Exit gate: a valid Arise-only plan passes, an invalid Arise plan fails, and a
valid Portage-only plan fails with an actionable Arise deficiency.

### Stage 6: execution and continuous expansion

- [ ] Execute selected fixtures in disposable roots.
- [ ] Compare predicted and actual final state.
- [x] Capture live differences as atomic, replayable frozen documents and
  reduce their available-package corpus to the candidates selected by each
  plan.
- [ ] Promote reviewed live differences into fully minimized hermetic fixtures.
- [ ] Track corpus coverage by semantic feature rather than package count.
- [ ] Archive superseded host-specific evidence once portable gates replace it.

`arise-plan-compare --capture-dir DIR` now asks Arise for the exact fixture and
plan used by its independent validation, translates emerge's displayed actions
against that same frozen package authority, validates both plan impacts, and
atomically publishes both state documents, the classification policy, and
capture metadata. Existing destinations are never overwritten. The stored
fixtures retain the complete installed baseline and final-state domains while
discarding unrelated repository candidates; further dependency-closure
reduction remains required before a host discovery becomes a hermetic corpus
fixture.

Exit gate: at least one classified `arise-valid-portage-fails` case passes
independent validation, mutation gates and disposable-root execution before any
public claim that Arise solves a graph Portage cannot.

## Completion criteria

This plan is complete when:

1. no routine test treats Portage action equality as the correctness oracle;
2. every executable Arise plan in the regression corpus passes independent
   final-state validation;
3. every omitted visible candidate has a bounded structured outcome;
4. the differential harness classifies valid differences instead of rejecting
   them;
5. mutation tests prove the validator detects missing, unnecessary and invalid
   actions;
6. live discoveries have a documented reduction path into hermetic fixtures;
   and
7. superiority claims require disposable-root execution in addition to
   independent validation.
