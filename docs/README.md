# Arise documentation

## Using Arise

- [Project overview](../README.md): installation, common commands, and scope.
- [Native tool equivalents](tool-equivalents.md): package queries, maintenance,
  coexistence, and Gentoo interoperability.
- [Configuration and state](configuration-layout.md): current paths and the
  distinction between implemented configuration and proposed layout.
- [Bug reports](bug-report.md): local collection, redaction, review, and export.
- [Fresh stage3](fresh-stage3.md): the runbook for the still-open fresh-system
  maintenance gate; it is not proof that the gate has passed.
- [Handbook addendum](handbook-addendum.md): unofficial amd64 installation
  guidance and Git repository configuration.
- [Man page](../arise.1) and [Info manual](../arise.texi): installed references.

## Development and validation

- [Development](development.md): builds, architecture, and environment.
- [Test lanes](testing/TEST_LANES.md): ordinary tests, host capabilities,
  disposable-root checks, and opt-in Portage comparisons.
- [Coverage](testing/COVERAGE.md): commands and dated coverage measurements.
- [Compatibility contract](../COMPATIBILITY.md) and
  [compatibility matrix](compatibility/PORTAGE_COMPATIBILITY_MATRIX.md): required
  behavior and the supported scope of individual interfaces.
- [eix parity](eix-search-parity.md): tested and incomplete search behavior.
- [Performance results](performance-results.md) and
  [benchmark matrix](../BENCHMARK_MATRIX.md): methodology and dated measurements.
- [Release workflow](../misc/RELEASE.md): artifact and overlay orchestration.
- [Architecture decisions](adr/README.md): accepted decisions and revisit criteria.

## Remaining work

The [punchlist](../PUNCHLIST.md) contains only unfinished work, ordered by
priority, with completion criteria. Remove finished entries; Git retains their
history. [Topic plans](planning/README.md) provide design detail for open items
and do not establish current support by themselves.

The [library roadmap](library-roadmap.md) and
[transaction backend design](transaction-backends.md) distinguish proposed
interfaces from implemented guarantees. Experimental designs must not be read
as available command-line options.

## Reviews and historical evidence

[Reviews](reviews/) record scoped findings and verification for particular
source revisions. [Audits](audits/README.md), [evidence](evidence/README.md), and
[release notes](releases/) retain dated results; none describes the current
installed host or certifies a later revision. Preserve machine-readable data
needed to reproduce a claim.

[Archived material](archive/README.md) is not current instruction. Do not add
an archive copy merely to preserve completed work; prefer Git history. Before
removing a document, retain unfinished requirements in the punchlist or an
active topic plan and repair inbound links.

## Documentation checks

Run `./support/check-docs.sh` after changing documentation or CLI behavior. It
checks local Markdown link targets, Bash syntax, and whitespace, compiles the
Info manual when `makeinfo` is available, and lints the man page when `mandoc`
is available. Missing optional tools are reported. External URLs are not
network-validated by this local check.

The ordinary Go suite also checks documented CLI routes and punchlist structure.
Keep examples aligned with executable behavior; tests should reject obsolete
instructions rather than require their wording to survive.
