# Dependency lifetime audit, 2026-09-08

The reported `arise` installation failed because independent validation treated
an unchanged installed package's build dependencies as final-state requirements.
The local VDB records `app-crypt/age-1.3.1-r1` as already installed, with
`BDEPEND=>=dev-lang/go-1.24.11:0/1.26.5= app-arch/unzip` and empty runtime
dependencies. Its appearance in the error did not mean Arise selected age for
installation.

## Corrections

- Retained packages no longer require historical DEPEND, BDEPEND, or IDEPEND.
  Their RDEPEND and PDEPEND remain checked. Source installs and reinstalls still
  validate their build and installation dependencies.
- Frozen available metadata and install actions preserve source/binary merge
  type. Binary installation does not require source build dependencies, but
  still checks IDEPEND, RDEPEND, and PDEPEND. Unknown merge types and a plan
  changing the frozen merge type fail validation.
- An existing dependency failure cannot be waived when the affected package is
  explicitly reinstalled. Untouched pre-existing runtime failures retain their
  existing impact classification.
- Committed-state prediction preserves retained VDB and prebuilt binary
  dependency metadata. Only newly built source metadata receives slot-operator
  binding and normalization.
- DEPEND uses BROOT before EAPI 7 and SYSROOT from EAPI 7 onward, matching the
  resolver's explicit cross-root model.
- Dependency diagnostics include the dependency class in JSON and human output.

## Coverage and verification

Regression matrices cover all five dependency classes, retained versus rebuilt
packages, source versus binary installation, unsupported and forged merge types,
EAPI 6/7/8 domains, and preservation of retained/prebuilt metadata. Adapter tests
cover frozen evidence and JSON round trips. A CLI audit fixture installs Arise
and updates Go while retaining age with the exact reported compiler constraint;
it passes through target canonicalization and the independent audit route.
The fixture uses a synthetic Go 1.26.5 to 1.26.6 transition, not a claim about
the user's actual selected Go upgrade.

Existing schema, adversarial-input, API, mutation, and atomicity tests remain in
the full suite. Dependency-domain fixtures now model actual source installation
rather than relying on historical build dependencies of retained packages.

The full Go suite and go vet passed. Race-enabled tests passed for the
validator, adapter, and CLI. Five real source mutations were rejected by
the focused regressions: restoring historical build checks, restoring binary
build checks, waiving reinstall failures, rewriting retained metadata, and using
the wrong legacy dependency root. Complexity remains within the existing
ratchets: average 7.87, 19 functions above 50, maximum at most 215.

This audit covers dependency lifetime, frozen plan evidence, diagnostics, and
committed metadata prediction. It does not claim exhaustive correctness of the
resolver or simulate every intermediate build state in cross-root transactions.
No host package state was changed and these fixes are not part of the already
published 0.0.31 release.
