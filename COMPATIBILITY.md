# Compatibility contract

Arise treats Portage, the Package Manager Specification, repository metadata,
and supported EAPIs as moving compatibility targets. Passing today's fixtures
is not sufficient evidence of parity.

## Test layers

| Layer | Purpose | Required behavior |
|---|---|---|
| Parser fixtures | Historical and synthetic future EAPIs | Preserve unknown EAPIs and metadata keys without data loss |
| Property and fuzz tests | Atoms, versions, dependency expressions, ordering | Deterministic results and round-trip invariants |
| Repository snapshot tests | Current Gentoo and selected overlays | Preserve every repository CPV and repository identity |
| VDB snapshot tests | Installed package state | Match every installed CPV, slot, repository, USE value, and dependency field |
| Portage differential tests | Current stable Portage | Match visibility, effective USE, dependency plans, and actions |
| Upgrade tests | New Go, Portage, EAPI, and repository revisions | Detect changed behavior before release |

Indexing is intentionally forward-compatible: an unknown EAPI remains visible
and lossless in the database. Execution is intentionally fail-closed: Arise
must reject an EAPI it has not explicitly declared executable before fetch,
build, or filesystem mutation.

Every compatibility failure should become a minimized permanent regression
fixture. Differential tests record the Portage version, repository commit,
profile, configuration digest, VDB digest, architecture, and EAPI corpus so a
change in the reference can be distinguished from an Arise regression.

The release gate will eventually run against both the oldest declared Portage
reference and current stable Portage. A scheduled current-Gentoo run should
warn when the repository introduces a previously unseen EAPI or metadata key,
even if lossless indexing continues to work.
