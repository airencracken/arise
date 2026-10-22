# Shared libraries

## Shared module: gentooling

[github.com/airencracken/gentooling](https://github.com/airencracken/gentooling):
Reusable Go libraries for Gentoo system and package tooling.
Arise and Maize should become peer applications over cohesive public APIs.

The first extracted surface provides explicit system paths and root-aware
inventory. Consumers choose partial inspection with typed issues or strict
validation. Arise consumes this API for its VDB scans.

| Boundary | Contract |
|---|---|
| Gentooling `v0.1.0` | Validated inventory, structured IUSE defaults, symlink-safe reads, opt-in CONTENTS, bounded concurrent record scans and final mutation revalidation |
| Gentooling `v0.2.0` | Profile graph with root-to-leaf layers, repository roots, policy and source-line provenance; rejecting parent escapes, cycles and symlinks. Arise now uses this graph |
| Gentooling `v0.3.0` | Effective configuration with caller-supplied environment rather than reading process state implicitly. Arise search now consumes this API |
| Gentooling `v0.4.0` | Atom/version parsing and package-specific effective USE provenance |
| Gentooling `v0.5.0` | World/system selections and locked combined snapshots |

The pre-1.0 compatibility policy allows documented minor-release breaks and
compatible patches. Further extraction waits for stable behavior and a concrete
consumer; internal packages are not public wholesale.

The profile-inspection feature now consumes Gentooling's profile inspection,
catalog and comparison APIs. Gentooling owns direct selected-directory loading,
assignment evaluation and provenance, selector validation, advertised profile
metadata, and ordered policy comparison. Arise retains repository-configuration
adaptation, command handling, terminal rendering and its `arise.profile.*` JSON
contracts. The inspection's profile-scoped configuration uses the existing
Gentooling package USE and visibility evaluators.

Gentooling variable explanations retain the expression, expansion, source line,
effective before/after value and prior defined state for each assignment. This
supports explanation without reconstructing history from a flattened result.
Package-mask reasons and global USE force/mask source lines remain inspectable.
The consumer contract is exercised both from Gentooling's external-package tests
and Arise's command/schema tests. Dependency solving and hashed execution plans
remain Arise responsibilities; a profile configuration diff does not plan a
profile migration.

## Concrete downstream consumer: Maize

Maize is planned hardware- and package-informed kernel configuration/upgrade
tooling, not a requirement to merge kernel tooling into Arise. If Maize must
execute `arise` and parse human output, a needed library boundary is missing.
Candidates include Portage configuration loading and effective per-package
policy, dependency and reverse-dependency inspection, and hardware- and
package-capability discovery.

Public code accepts `context.Context`, explicit roots and policy; separates
inspection, planning, mutation and presentation; returns deterministic typed
results and diagnostics; and must remain usable with `CGO_ENABLED=0` without
Python, Portage or the Arise executable. Require a downstream compile test,
examples and versioned compatibility policy before promotion.

A future kernel build and installation tool owns build/initramfs/bootloader
operations; Maize owns kernel policy. Neither concern belongs in Gentooling's
package-state contracts. Generic solver extraction has its own
[boundary constraints](planning/SOLVER_LIBRARY_PLAN.md).
