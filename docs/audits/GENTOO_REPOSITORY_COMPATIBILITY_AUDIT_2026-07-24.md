# Gentoo Repository Compatibility Audit — 2026-07-24

## Scope

This is a read-only audit of the complete local Gentoo repository at
`/var/db/repos/gentoo`, not merely installed packages or the current `@world`
plan.

The repeatable command is:

```sh
go run ./cmd/arise-repo-audit \
  -repo /var/db/repos/gentoo \
  -worker internal/phaseproto/worker.sh \
  -output docs/audits/gentoo-repository-compatibility-2026-07-24.json
```

The JSON report is the detailed evidence. The command exits unsuccessfully
only when the audit itself cannot complete; findings remain available for
classification instead of making a changing upstream tree unusable in CI.

## Repository baseline

- 32,426 ebuilds scanned
- 212 eclasses scanned
- EAPI 7: 2,816 ebuilds
- EAPI 8: 29,190 ebuilds
- EAPI 9: 420 ebuilds
- 0 parser failures after repairs
- 0 missing statically named eclasses
- 0 static eclass inheritance cycles
- 0 referenced package-manager helpers absent from the Arise phase worker

The helper check is intentionally limited to package-manager-provided helpers.
Functions supplied by eclasses are not phase-worker primitives.

## Defects found and repaired

### Incomplete transitive eclass discovery

The metadata parser intentionally skips commands inside shell conditionals.
That made it insufficient as the sole source of eclass reachability:

- 122 eclasses contain static inheritance names not returned by the metadata
  parser.
- 4,746 ebuilds contain such differences, primarily conditional live-version,
  feature, or implementation inherits.

Arise preflight now conservatively unions syntactically static inherit names
from both the ebuild and every transitively reached eclass. Runtime Bash still
decides which conditional inherits actually execute. This closes the class of
failure that omitted `cargo.eclass -> rust.eclass` and the Rust slot probes used
by cbindgen.

### Ebuild structural parser edge cases

The whole-tree pass found valid Bash constructs that corrupted the parser's
function-brace depth:

- brace expansion inside phase functions;
- nested helper functions inside phase functions;
- a trailing backslash inside a shell comment;
- braces inside heredoc bodies;
- heredoc-looking text inside quotes;
- here-strings (`<<<`) being confused with heredocs.

The parser now distinguishes structural shell braces and skips heredoc bodies.
All 32,426 current ebuilds parse successfully.

## Version-query inventory

The tree contains 3,250 textual version-query sites:

- 1,905 literal arguments
- 1,320 variable-derived arguments
- 25 command-substitution/computed arguments
- 2,321 `has_version` calls
- 185 `best_version` calls
- 744 `python_has_version` calls

This is an inventory, not 1,345 confirmed failures. Arise already materializes
finite query families for Python implementations and USE dependencies, Rust
slots, LLVM slots, Vala slots, Qt identity variables, and autotools versions.
It also expands stable ebuild identity variables such as `CATEGORY`, `PN`,
`P`, `PF`, and `PV`.

The argument-driven and computed families exposed the architectural ceiling of
static enumeration. Arise now treats the static answers as a cache rather than
a correctness boundary. A cache miss invokes the same frozen Arise executable
through a narrow, read-only interface which accepts only `has-version` or
`best-version`, a ROOT/BROOT domain, an atom, and the caller's USE set.

The runtime matcher evaluates version, slot/subslot, repository, installed
USE/IUSE state, USE defaults, and caller-conditional USE dependencies. Query
cache keys are now domain-qualified, so the same atom can produce different
answers in ROOT and BROOT without collision.

Live checks against `portageq` agreed for positive and negative version, slot,
USE-qualified, missing-slot, and USE-default atoms, as well as `best_version`.
The older Python/Rust/LLVM/Vala/Qt/autotools expansions remain temporarily as
cache optimizations, but correctness no longer depends on them. They can be
removed separately after a real package-build canary exercises the fallback.

## What this audit does not prove

- It does not execute every ebuild phase or external build tool.
- It does not build every package/USE/architecture combination.
- It does not prove semantic parity for every eclass-defined function.
- A helper being present does not prove all of its EAPI-specific semantics.
- Conditional inheritance differences are conservative reachability evidence,
  not proof that every conditional branch executes for a package.

Those require separate runtime canaries, Portage differential tests, and
eventually broader build sampling. The successful cbindgen repair is one such
runtime canary for the transitive-inheritance/query-discovery fix.

## Validation

- Targeted parser, rebuild, and repository-audit tests pass.
- `go test ./...` passes, including loopback HTTP tests when run outside the
  network-restricted development sandbox.
- The final whole-tree report has zero parse failures.
