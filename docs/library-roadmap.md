# Public library direction

Arise does not yet promise a public Go API. Packages beneath `internal/` cannot
be imported by downstream applications, and moving them merely to make them
importable would freeze immature boundaries too early.

New work should nevertheless preserve a direct path to public libraries:

- core operations accept typed inputs, contexts, explicit paths, and explicit
  policy rather than reading CLI globals;
- core operations return typed results and errors rather than printing,
  prompting, or terminating the process;
- filesystem mutation is separated from discovery and planning;
- deterministic result ordering is part of the contract;
- line, JSON, TUI, and future API representations remain adapters over the
  same result types; and
- interoperability formats are explicit at package boundaries.

The command package owns argument parsing, terminal presentation, and exit
status. It should remain thin enough that a future Charmbracelet application
can call the same underlying operation without emulating an Arise subprocess.

## Concrete downstream consumer: Maize

Maize is a separate planned Gentoo project written in Go. Its purpose is to
help generate hardware- and package-informed "optimal" kernel configurations
and to assist with custom-kernel upgrades. It is a concrete future consumer of
Arise libraries, not a requirement to merge kernel tooling into Arise.

Maize should eventually be able to import Arise packages to answer questions
such as:

- which kernel features are required by installed or proposed packages;
- which CPU, firmware, filesystem, networking, security, virtualization, and
  device options fit the detected machine and package state;
- what changed between the running kernel, installed kernel sources, and a
  proposed kernel upgrade;
- which package changes make a kernel option newly required or obsolete; and
- whether a proposed kernel can support the current or planned package state
  before either package or kernel installation begins.

If Maize must execute `arise` and parse human output to obtain package state,
that is evidence of missing public library surface. Subprocess interoperability
remains valuable, but it must not be the only supported integration boundary.

## Candidate public surfaces

The Maize use case makes the following extraction candidates concrete:

- Portage configuration loading and effective per-package policy;
- repository and installed-package metadata snapshots;
- atom and Gentoo-version parsing and matching;
- dependency and reverse-dependency inspection;
- USE, keyword, license, repository, slot, and subslot state;
- hardware- and package-capability discovery inputs and typed findings;
- immutable transaction or package-state planning without mandatory execution;
  and
- structured diagnostics, explanations, and provenance.

These do not need to become one large package or be exported immediately.
Boundaries should follow cohesive data ownership and dependency direction.
Generic solver packages must not import Gentoo filesystem or presentation
concerns; Gentoo semantic packages may compile normalized inputs for them.

## External-consumer design constraints

Code likely to become public should:

- accept `context.Context`, explicit roots, repository paths, configuration,
  architecture, and policy rather than assuming `/` or reading process-global
  flags;
- avoid hidden environment reads and make any environment-derived defaults
  available through explicit constructors;
- return stable typed values, structured diagnostics, and ordinary errors;
- keep discovery, planning, mutation, prompting, logging, and rendering
  separable;
- remain usable with `CGO_ENABLED=0` and without invoking Python, Portage, or
  the Arise executable;
- preserve deterministic ordering and serializable identities for fixtures,
  caching, comparison, and TUI state updates; and
- avoid importing `cmd/arise`, terminal packages, or concrete CLI renderers.

Promotion to a public package should include a downstream compile test that
uses only exported APIs. Maize can become that real downstream contract once
its repository is ready; until then, a small external-style test module or
example should enforce the same import direction.

Once a package boundary has demonstrated stability through CLI, integration,
property, and adversarial contracts, it can be promoted from `internal/` to a
versioned public package. That promotion should include API documentation,
compatibility policy, examples, and downstream import tests.

Public-library work is not required for the current query-parity release. Code
that unnecessarily couples new core behavior to `cmd/arise`, global flags, or
terminal output is still considered architectural regression.
