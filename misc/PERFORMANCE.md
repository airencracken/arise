# Arise performance comparisons

Arise performance claims must be correctness-gated. A comparison is valid only
when both tools run against the same repository, profile, Portage configuration,
installed state, cache mode, and requested operation, and their normalized
results are equivalent.

## Harness

Build the standalone harness:

```sh
make perf-harness
```

Run a workload and identify the immutable snapshot used by both commands:

```sh
./arise-perf \
  -workload misc/perf-smoke.json \
  -snapshot 'gentoo-commit+profile+vdb-digest' \
  -output /tmp/arise-perf.json
```

The command exits unsuccessfully if any paired result is not equivalent. The
JSON report records raw samples, median and p95 wall time, user/system CPU,
maximum RSS, block I/O, output hashes, exit codes, speedup, host runtime data,
cache-file footprint, and the caller-supplied snapshot identity.

It also exits unsuccessfully when median speedup is below `min_speedup`, which
defaults to `1.0`. Real milestone workloads should use higher budgets wherever
Arise should have a decisive architectural advantage. A case marked
`report_only` still requires equivalent output and records the speed comparison,
but does not fail on speed; this is used for aspirational portage-utils targets.
Only harness self-tests may set the budget to zero.

`make perf-smoke` checks the harness itself. It is not an Arise-versus-Portage
performance claim.

Prepare a user-owned index for repository-query workloads with:

```sh
make perf-prepare
```

This writes `/tmp/arise-perf-data`; it does not alter the system Arise index.

## Workload format

```json
{
  "name": "example",
  "warmups": 1,
  "runs": 7,
  "cases": [
    {
      "name": "operation-name",
      "normalize": "sorted-lines",
      "min_speedup": 1.0,
      "report_only": false,
      "arise": {
        "path": "./arise",
        "args": ["installed", "--versions"],
        "cache_paths": ["/var/lib/arise/data"]
      },
      "reference": {
        "tool": "eix",
        "path": "reference-command",
        "args": ["equivalent", "arguments"]
      }
    }
  ]
}
```

Normalization modes:

- `exact` trims surrounding whitespace and compares the remaining output.
- `sorted-lines` additionally trims and sorts lines before comparison.
- `package-names` compares sorted package-name components, allowing a tool that
  prints `category/package` to be compared with one that prints only `package`.
- `search-package-names` extracts package names from Arise, eix, or emerge
  search output while ignoring headings and descriptive fields.
- `exit-code` compares exit behavior only. It is suitable for harness smoke
  tests, not feature-parity or release-performance claims.

Each command may name its `tool` and provide `version_args`; the resolved path
and first version-output line are stored in the report. Environment entries can
be supplied as an `env` string array on either command.
Commands are executed directly without a shell.

`cache_paths` lists files or directories whose regular-file sizes are summed
after the benchmark. Query comparisons against indexed tools must report both
cache footprints. Previous/snapshot caches not required by the measured query
are recorded separately rather than silently charged to either tool.

Index-build workloads may provide paired `arise_validate` and
`reference_validate` commands. Their normalized outputs—not incomparable build
logs—determine correctness. `reset_paths` removes only explicitly named paths
below the system temporary directory before each timed invocation, ensuring a
full-build sample does not reuse or accumulate prior database state.

`misc/perf-emerge-index.json` redirects Portage's dependency cache entirely to
`/tmp`, but `emerge --metadata` still requires portage-group/root authority.
Run that workload through the project's root benchmark workflow; do not weaken
Portage permissions or grant untrusted users membership merely to benchmark it.

## Benchmark policy

- Store workload definitions in version control.
- Record how the snapshot identity was generated.
- Do not compare synthetic Arise state with live Portage state.
- Report cold, warm, and incremental results separately.
- Use enough runs to make median and p95 meaningful.
- Investigate result differences before looking at speedup.
- Treat `portage-utils` q commands as aspirational native-speed targets: Arise
  should be on par or faster and must report losses honestly, but those losses
  do not fail builds. Decisive, enforced wins remain focused on emerge and eix.
- Never publish a speedup from an `exit-code`-normalized workload.
- Use `package-names` only when category ambiguity is impossible or separately
  checked by the workload.
- Keep raw JSON reports for significant milestone decisions.
