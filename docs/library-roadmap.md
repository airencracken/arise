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

Once a package boundary has demonstrated stability through CLI, integration,
property, and adversarial contracts, it can be promoted from `internal/` to a
versioned public package. That promotion should include API documentation,
compatibility policy, examples, and downstream import tests.

Public-library work is not required for the current query-parity release. Code
that unnecessarily couples new core behavior to `cmd/arise`, global flags, or
terminal output is still considered architectural regression.
