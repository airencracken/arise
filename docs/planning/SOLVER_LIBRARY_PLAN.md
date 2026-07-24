# Reusable pure-Go solver library plan

## Decision

Arise's dependency solver should become a reusable pure-Go library. The first
boundary will live inside this repository (for example, `pkg/solver`) until real
world-update experience proves that its API is independent of Arise. A separate
Go module or repository is a later packaging decision, not a prerequisite.

The library must preserve Arise's standalone recovery contract:

- no cgo;
- no libsolv linkage or runtime dependency;
- no Python, Portage, filesystem, VDB, repository, terminal, or CLI dependency;
- deterministic operation over an immutable input problem;
- compatibility with `CGO_ENABLED=0` static builds.

libsolv is an algorithmic and architectural reference only. Any future direct
integration would be an optional, USE-gated enhancement with the native Go
solver remaining a complete fallback; no such integration is currently
planned.

## Boundary

The intended data flow is:

```text
Gentoo repository/profile/VDB state
                |
                v
Arise Gentoo semantic compiler
                |
                v
normalized immutable solver problem
                |
                v
reusable pure-Go solver library
                |
                v
solution + conflicts + decision/explanation trace
                |
                v
Arise transaction planner and executor
```

Arise remains responsible for interpreting Gentoo and Portage semantics:

- atoms, versions, repositories, masks, keywords, licenses and profile policy;
- USE conditionals, defaults, forcing/masking and `REQUIRED_USE`;
- slots, subslots, slot operators, blockers and virtual/provider semantics;
- build, host, root and install dependency domains;
- world/system sets, installed-package policy and dynamic dependencies;
- preserved-library, ABI-rebuild and transaction-ordering edges;
- Portage-compatible rendering, execution and recovery.

The solver library receives already-normalized generic concepts:

- stable integer IDs for candidates, capabilities and rules;
- requirements and alternative groups;
- providers and mutually exclusive selections;
- conflicts and cardinality constraints;
- installed/current selections;
- legal-solution preferences and costs;
- externally supplied ordering constraints where relevant.

## Ideas to study and borrow from libsolv

- Compile dependency expressions into compact reusable rules rather than
  repeatedly interpreting source expressions during search.
- Use dense IDs, arrays and bitsets for cache-friendly traversal.
- Separate satisfiability from policy ranking: legality first, Gentoo choice
  policy second.
- Retain the reason for every rule, decision, rejection and implication.
- Learn from failed branches so equivalent contradictions are not revisited.
- Reuse immutable repository/solvable snapshots across resolutions.
- Support incremental rule regeneration for changed package metadata.
- Build an explicit post-solve transaction classification: install, update,
  downgrade, reinstall, replace and remove.
- Reduce ordering graphs through strongly connected components.
- Report minimal or near-minimal conflicting constraint sets.

These are design inputs, not a commitment to reproduce libsolv's RPM data
model or implementation.

## Comparative source raids

libsolv is not the only useful prior art. Maintain a structured audit of other
source-oriented package managers and record each borrowed idea with its source,
the problem it solves, and whether Gentoo semantics permit the same design.

### Paludis and cave

Paludis is especially relevant because it has supported both Gentoo ebuild/VDB
repositories and Exherbo's exheres format. Study:

- the separation between the Paludis library and modular `cave` client;
- package-ID, repository, environment and selection/filter/generator APIs;
- resolver decision, constraint, confirmation and failure representations;
- destination types and installed-versus-origin repository identity;
- rich package dependency specifications and metadata-key matching;
- user-facing explanations, suggested actions and verbose error structure;
- configurable output/logging without coupling presentation to the resolver.

Primary entry points:

- <https://paludis.exherbo.org/>
- <https://paludis.exherbo.org/configuration/specs.html>

### Exherbo and exheres

Exherbo provides a useful counterfactual: familiar source-package concepts with
semantics designed after early Portage experience rather than constrained to
ebuild compatibility. Study:

- explicit dependency annotations, especially blocker and recommendation
  explanations;
- alternatives, providers and virtuals;
- equals/star slot dependency distinctions;
- compound version ranges and option requirements;
- cross-compilation and multiarch domain modeling;
- multibuild abstractions and bootstrap workflows;
- which ambiguities exheres removed from ebuild semantics and which of those
  lessons can improve Arise's normalized frontend without changing Gentoo
  behavior.

Primary entry points:

- <https://www.exherbo.org/docs/index.html>
- <https://www.exherbo.org/docs/eapi/exheres-for-smarties.html>

### Historical Funtoo Portage and ecosystem

Treat Funtoo as historical design archaeology rather than a current independent
solver baseline. Its Portage fork pioneered changes that were later merged
upstream, and later Funtoo releases returned to an unmodified upstream Portage.
Study archived code and documentation for:

- resolver experiments and performance changes that differed from contemporary
  Gentoo Portage;
- Git/meta-repo and kit composition, update channels and repository grouping;
- `ego`'s separation of system personality/profile management from emerge;
- operational lessons from curated kit versions and cross-repository policy;
- ideas that disappeared, were upstreamed, or failed to survive long-term use.

Primary historical entry points:

- <https://www.funtoo.org/Package:Portage_(Funtoo)>
- <https://www.funtoo.org/Funtoo_Kits>
- <https://www.funtoo.org/Package:Ego>

### Audit discipline

For every source raid:

1. Read primary source and tests before secondary descriptions.
2. Extract concepts and invariants, not code or foreign data models wholesale.
3. Map the idea through Arise's Gentoo semantic frontend.
4. Build a minimal generic solver fixture and a Gentoo parity fixture.
5. Benchmark only after plan equivalence is proven.
6. Record rejected ideas and why they conflict with static, transactional or
   Portage-compatible requirements.

## Exported API principles

Explanations are first-class output. A useful API cannot end at
`Solve() ([]Selection, error)`. It must support:

- why a candidate was selected;
- why another candidate was rejected;
- which input rule produced an implication;
- which constraints form an unsatisfied problem;
- which policy preference broke a tie between legal solutions;
- the decision/backtrack trace needed for diagnostics and differential tests.

Inputs and outputs must be versionable, deterministic and serializable enough
for portable fixtures. Cancellation, explicit resource limits and bounded
decision budgets belong in the library API. Logging and presentation do not.

## Migration sequence

1. Document the current resolver's Gentoo semantic inputs and side effects.
2. Introduce normalized candidate/rule/reason types without changing results.
3. Move generic decision state and backtracking behind an internal solver API.
4. Add generic satisfiable, unsatisfiable, preference and explanation corpora.
5. Differentially prove unchanged Arise plans against current fixtures and
   representative Portage matrices.
6. Replace repeated string/map work with dense IDs and compiled rules, guided
   by profiles rather than assumed speedups.
7. Stabilize an exported `pkg/solver` API only after at least one deep/newuse
   world-update corpus and the conflict corpus exercise the boundary.
8. Consider a separate module only after another consumer can use the API
   without importing Gentoo-specific packages.

## Acceptance gates

- Arise's existing resolver parity corpus remains unchanged or improves.
- The library has no imports from Arise CLI, Portage configuration, VDB,
  repository loading, filesystem mutation or terminal packages.
- Static builds remain verified with `CGO_ENABLED=0`.
- Every unsatisfied result has structured rule provenance.
- Equivalent normalized inputs produce deterministic selections and traces.
- Performance claims use correctness-equivalent plans and the maintained
  benchmark matrix.
- Extraction does not weaken Gentoo semantics merely to fit a generic SAT
  representation.
