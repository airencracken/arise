# Coverage

```sh
make test-coverage
make test-coverage-html
go test ./cmd/... ./internal/... -cover -count=1 -timeout=60s
```

The maintained core lane instruments production packages but excludes test
execution in benchmark, binary-package, fetch and live-integration packages.
It writes `/tmp/arise-coverage.out` and `/tmp/arise-coverage-functions.txt`.
The package-local command measures each package's own tests; those percentages
are not directly comparable with whole-tree core coverage.

Retain the source revision, Go version, instrumentation, exclusions and skips
with each measurement. [Dated JSON snapshots](../evidence/README.md) are
historical baselines, not coverage claims for the current release.

Choose tests by failure risk: CLI routes and side effects; lifecycle and protocol
contracts; hostile paths and parser inputs; interrupted durable writes;
concurrent scheduling and commit proof. Combine unit, integration, property,
fuzz, mutation and atomicity checks where relevant. A higher percentage alone
is not evidence of a correct transaction.

See [test lanes](TEST_LANES.md) for capabilities and opt-in comparisons.

`make test-mutation` runs input/state mutation tests. For actual source mutation:

```sh
go install github.com/jonbaldie/go-mutesting/v2/cmd/go-mutesting@v2.7.9
make test-mutation-analysis
make test-mutation-analysis MUTATION_TARGETS=internal/journal \
  MUTATION_MATCH=coveredByAbsentTree
```

The pinned runner is optional. Review surviving mutants; a tool error is not
evidence that a mutation was detected. Preserve lane/exclusion identity when
comparing coverage snapshots and keep percentages within 0–100.
