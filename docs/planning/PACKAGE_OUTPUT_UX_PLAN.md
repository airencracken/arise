# Package output

Current output distinguishes plan action/type/selected and replaced identities,
USE transitions and download sizes. Successful distfile details are suppressed
in default execution output and retained by `--verbose`; automatic redirected
output and JSON remain uncolored.

Keep Portage's default record grammar and attribute/USE marker semantics as the
reference. Preserve source/binary type, CPV, slot/subslot, repository, domain,
installed identity, rebuild cause, masks and download requirements. Group
USE_EXPAND and retain changed/new/removed/forced/masked distinctions without color.
Render the immutable authorized action rather than rereading mutable policy.

Runtime stages must distinguish `Installing into staging area`,
`Validating package contents`, content installation/sync, commit and post-install
work. Completed means committed; post-commit failures remain distinguishable.
Default successful fetch output is bounded by package rather than distfile count.
Never truncate recovery operands or identity fields; wrapping is deterministic.

Acceptance: source/binary, upgrade/downgrade/reinstall/slot/remove snapshots,
USE_EXPAND and long rows, exact download sums, escape-stripped equality,
parallel progress and actionable failure/recovery transcripts.
