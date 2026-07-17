# Additional command-line reference tools

This inventory is derived from the active Gentoo repository snapshot. These
packages are not prerequisites for Arise and should not be installed blindly;
they are candidates for independent fixture views and competitive comparisons.

## Available high-value references

- `app-portage/emlop-0.8.1`: installed independent fast emerge.log parser;
  capture history and statistics alongside genlop, qlop and future Arise logs.
  Prediction and accuracy fixtures need controlled synthetic log/pretend data.
- `app-portage/golop-0.2.1-r1`: installed Go implementation of genlop; capture
  history alongside the other parsers and review it as prior art for reusable
  Go Portage libraries. Its `-v` path attempts to open the emerge log first on
  this version, so package metadata—not `golop -v`—records its fixture version.
- Installed `e*` references include Portage's `ebuild`, `egencache`, `emaint`,
  `emerge`, `emirrordist`, and `env-update`; Gentoolkit's `ebump`, `eclean`,
  `ekeyword`, `enalyze`, `epkginfo`, `equery`, `eshowkw`, and `euse`; the eix
  command family; and `eselect`. Many are Python entry points on this machine.
  Capture read-only success or failure signatures now, and use native/shell
  comparators where the damaged Python control plane prevents deeper queries.

## Highest-value missing candidates

- `app-portage/recover-broken-vdb`: focused VDB inconsistency detection for the
  recovery and damaged-system fixture corpus.
- `app-portage/elicense`: independent installed-license policy findings.
- `app-portage/euses`: fast USE-description lookup for search/info parity.
- `app-portage/esearch`: indexed command-line search comparator alongside eix,
  emerge search, qsearch and Arise.
- `app-portage/smart-live-rebuild`: live-package update detection and pretend
  planning, relevant to resolver and maintenance parity.
- `app-portage/diffmask`: mask/unmask maintenance behavior and stale-rule
  diagnostics.

## Configuration and maintenance candidates

- Portage's installed `dispatch-conf` and `etc-update` are the authoritative
  configuration-update comparators. The default harness captures capability
  output only; decision, archive and mutation semantics require an isolated
  disposable `ROOT`, never the live host configuration.
- `app-portage/flaggie`: package.* editing semantics; only dry-run/query-safe
  modes belong in reference capture.
- `app-portage/cfg-update` and `app-portage/conf-update`: independent
  CLI/config-file update behavior for eventual dispatch-conf replacement work.
- `app-portage/mirrorselect`: mirror selection and make.conf representation.
- `app-portage/portpeek`: keyword/unmask maintenance diagnostics.
- `app-portage/unsymlink-lib` and `app-portage/time64-prep`: specialized Gentoo
  migration planners useful for the future migration-advice corpus.
- `app-portage/pkg-testing-tools`: isolated package test workflows and fixtures.

## Development and QA references

- `app-portage/overlint`, `app-portage/nattka`, `app-portage/tatt`,
  `app-portage/iwdevtools`, `app-portage/mgorny-dev-scripts` and
  `app-portage/eschwartz-dev-scripts` expose repository QA policy, but are not
  package-manager parity gates.
- `app-portage/gentoopm` is an API abstraction rather than a primary CLI
  reference; review it when stabilizing public Arise libraries.

## Exclusions

Graphical-only frontends are excluded. TUI/package-manager wrappers such as
`carnage`, `pupgrade` and `epkg` may inform UX later, but they are not
authoritative semantic references. Online services such as `pfl` are unsuitable
for immutable offline fixtures unless their data is separately snapshotted.

Before installing any candidate, record the exact package version and intended
read-only commands in the fixture manifest. Never add commands that sync,
generate, fix, clean, merge, unmerge or rewrite configuration to the default
capture lane.
