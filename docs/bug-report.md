# Bug reports

`arise bug-report` creates a private local directory containing `report.md`
and `report.json`. It never uploads data, opens an issue, or contacts a remote
service.

```sh
arise bug-report --output arise-bug-report
```

Review both files before sharing them. To additionally create a deterministic
archive:

```sh
arise bug-report --output arise-bug-report \
  --archive arise-bug-report.tar.zst
```

Use `--package category/package` to associate an explicit package and
`--log PATH` to include a specific durable package log. Log collection is
bounded to 1 MiB per file. The default `--latest-failure` behavior includes
the latest interrupted package log when one exists.

The collector uses an allowlisted schema. It does not copy the process
environment or arbitrary Portage configuration. Usernames, the home path,
hostname, URL credentials and queries, and values labeled as passwords,
tokens, secrets, cookies, authorization, or proxies are redacted before either
output format is produced. Output directories use mode `0700`; files and
archives use `0600`. Existing output is never overwritten.

The machine-readable contract is
[`bug-report-v1.schema.json`](schemas/bug-report-v1.schema.json).

Collection is read-only. Missing optional state is omitted, while malformed
resume, journal, log, or filesystem state produces a visible warning and marks
the report incomplete.

For resolver failures, create a separate privacy-reviewed trace while resolving:

```sh
arise --pretend --save-resolver-trace resolver-trace.json @world
```

The trace file is created with mode `0600`, is never overwritten, and contains
only bounded resolver decisions and redacted diagnostics. Review it before
attaching it alongside a bug report.

To embed it directly after review:

```sh
arise bug-report --output arise-bug-report \
  --resolver-trace resolver-trace.json
```

The importer strictly decodes and semantically validates the trace, applies the
redactor again, and fails before publishing any report when the trace is
malformed. The embedded trace appears in `report.json` and therefore in the
deterministic archive as well.
