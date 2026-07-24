# Support tooling

This directory contains maintained developer and evidence-capture utilities,
not product commands or one-host mutation drivers.

- `check-docs.sh` validates maintained documentation and shell syntax.
- `lib/error-handling.sh` provides side-effect-free command checks and
  contextual status propagation.
- `perf/` contains correctness-gated resolver profiling harnesses.
- `fixtures/` contains read-only reference-fixture capture tooling.

Disposable live-host canaries and transaction continuation drivers do not
belong in the repository. Their durable results should be reduced to tests,
dated evidence, and maintained operational contracts.

First-party shell tools must not enable global `errexit`, `nounset`, or
`pipefail` modes. Required failures are checked at the command boundary;
best-effort evidence generation remains visibly best effort.
