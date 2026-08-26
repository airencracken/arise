# Cyclomatic complexity audit, 2026-08-26

## Scope and method

The audit covers production Go files under `cmd`, `internal`, and `misc`, with
test files excluded. Measurements use `gocyclo` 0.6.0. Run the ratchet with:

```sh
GOCYCLO=/path/to/gocyclo make audit-complexity
```

The initial baseline was an average complexity of 7.91, 21 functions above 50,
and three above 100. The highest-complexity functions were:

| Complexity | Function |
|---:|---|
| 221 | `cmd/arise.runResolve` |
| 176 | `resolve.(*resolver).planPackage` |
| 146 | `merge.merge` |
| 97 | `resolve.(*resolver).verifyPlannedStatePass` |
| 90 | `rebuild.rebuildWithPhaseProtocol` |
| 85 | `resolve.(*resolver).dependenciesForVersion` |
| 76 | `resolve.(*resolver).processAnyOf` |
| 76 | `phaseproto.runWorkerCommandWithOptions` |

## Changes in this pass

`phaseproto.Request.Validate` was reduced from 56 to 12 by separating command,
token, path, and package-identity validation. The successful validation path
also improved from approximately 1,420 ns and three allocations to 760 ns and
zero allocations. A zero-allocation property test and benchmark now protect
that result.

The resulting repository baseline is an average complexity of 7.89, with 20
functions above 50. The ratchet rejects increases to either value and rejects
any function above the existing maximum of 221.

## Performance acceptance rule

Lower complexity is not sufficient justification for a change. Every hot-path
refactor must retain or improve representative wall-clock benchmarks,
allocations, and production-style resolver timings. A proposed extraction of
`planPackage` lowered its score from 176 to 124 but increased forced-backtrack
allocations from 6,096 to 6,137 per operation and bytes from roughly 441 KB to
442 KB. That refactor was rejected and is not included.

Future work should address the remaining hotspots in measured slices, starting
with `runResolve`, `planPackage`, and `merge`, while preserving this performance
rule and the existing behavioral, atomicity, and adversarial test suites.
