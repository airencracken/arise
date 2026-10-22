# Documentation

## Using Arise

- [Install and quick start](../README.md)
- [Command and tool equivalents](tool-equivalents.md)
- [Configuration and state paths](configuration-layout.md)
- [Profile inheritance and policy inspection](profile-inspection.md)
- [Supported behavior](compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
- [Bug reports](bug-report.md)
- [Man page](../arise.1) and [Info manual](../arise.texi)
- [Release notes](releases/README.md)

## Development

- [Build, architecture and conventions](development.md)
- [Test lanes](testing/TEST_LANES.md) and [coverage](testing/COVERAGE.md)
- [Compatibility contract](../COMPATIBILITY.md)
- [Search parity](eix-search-parity.md)
- [Performance methodology and results](performance-results.md)
- [Release workflow](../misc/RELEASE.md)
- [Architecture decisions](adr/README.md)
- [Open work](../PUNCHLIST.md) and [design constraints](planning/README.md)
- [Shared library boundary](library-roadmap.md)

## Acceptance procedures and evidence

The [fresh-stage3 runbook](fresh-stage3.md) and
[Handbook addendum](handbook-addendum.md) describe experimental installation
and validation procedures. The full fresh-stage3 acceptance run is deferred
for 0.0.42; neither procedure is evidence that it passed.

The [0.0.42 review](reviews/2026-10-11-stabilization-release.md) records tested
scope and remaining gaps. [Machine-readable evidence](evidence/README.md)
retains dated measurement inputs. Superseded prose lives in Git history.

Run `./support/check-docs.sh` after documentation changes. It checks local
links, Bash syntax, whitespace, Info compilation and man-page lint. Optional
tools report skips; external URLs are not checked. The Go suite also checks
CLI examples, versions, schemas and compatibility contracts.
