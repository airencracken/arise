# Diagnostic intelligence

Arise's resolver trace, plan comparison and configuration doctor share one
boundary: diagnostics are read-only evidence, never permission to mutate policy
or execute a package plan.

## Resolver traces

Resolver traces use a versioned, bounded JSON document. Every free-form string
passes through the support-report redactor before encoding. Decoding rejects
unknown fields, trailing values, unsupported schemas and oversized documents.
The trace carries candidate and backtracking evidence, not repository files,
environment variables or arbitrary configuration contents.

Install and update resolution export traces with `--save-resolver-trace`. The
command writes with mode `0600`, refuses overwrites and emits failed-resolution
evidence as well as successful traces. `bug-report --resolver-trace` validates
and redacts imported traces again before embedding them in its private JSON and
archive. Trace schema evolution must remain backward-readable or use a new
schema version.

## Plan comparison

Plan differences operate on frozen validation plans rather than terminal
output. Actions are compared by stable identity and classified as added,
removed or changed, with changed fields named explicitly. Future persistence
must bind each saved plan to its state and repository fingerprints so a diff
never implies that two solutions were produced from equivalent inputs when
they were not.

`arise plan-diff [--json] BEFORE AFTER` compares bounded, strictly decoded
saved Arise plans. Its stable identity excludes version and USE so those appear
as one changed action rather than an artificial removal and addition.
Operation, target, completeness, state-digest and plan-digest drift is reported
separately from package action changes.

## Configuration doctor

Doctor findings are deterministic records with severity, rule position and
related-rule evidence. The first pass covers ordered `package.use` rules. Later
passes will add overlapping atom analysis, IUSE validation, other `package.*`
families, selected-set obsolescence and repository drift.

The doctor may propose text for review, but it must not edit configuration as a
side effect of inspection. Any future repair mode requires a separately
reviewed atomic write plan with conflict detection and rollback.

`arise doctor package-use` reads the effective Portage configuration and the
current installed/visible resolver graph. `package-policy` audits the ordered
accept-keywords, license, env, mask and unmask families; `all` combines both
passes. `world` audits invalid, duplicate and obsolete selected targets while
preserving set references. Global `--json` selects the versioned
machine-readable report.
