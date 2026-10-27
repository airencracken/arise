# History and timing

`arise history` reads Portage-compatible merge logs without opening the package
index or changing installed state. It uses `/var/log/emerge.log` by default,
respecting the global `--emerge-log` option. The log must be readable.

```sh
arise history
arise history --since 7d --kind merge
arise history stats firefox
arise history stats --since 2026-10-01 --until 2026-10-27 'dev-lang/*'
arise history incomplete
arise history --log /var/log/emerge.log.1.gz --log /var/log/emerge.log --utc
arise history --json --limit 0
arise --show-estimates -pvuDN @world
arise --show-estimates --jobs 4 -avuDN @world
```

Put history options before package filters. Filters match a package name,
category/package, exact CPV, or quoted shell glob. Multiple filters are ORed.
`list` is the default mode; `stats` sorts package/action/repository/root groups
by total work, and `incomplete` shows unfinished or interrupted actions.
The default limit is 20 rows; `--limit 0` displays all matching rows.
Statistics use all matches before the display limit is applied.

Rows show dates, actions, durations and package/repository identities.
Summaries show completed runs, timed samples, median, average, p90 and total
recorded work. The median averages the two middle samples for even counts;
p90 uses the nearest-rank method. JSON retains fractional seconds for median
and average; text compacts durations for display. Total work is the sum of package
durations, so parallel work is not a measurement of elapsed wall time.
Groups never combine explicitly different repositories or installation roots.

Dates are local unless `--utc` is selected. `--since` is inclusive and `--until`
is exclusive for RFC3339 timestamps. A date-only `--until` includes that whole
calendar day. `today`, `yesterday`, positive Go durations such as `12h`, and
calendar-day/week offsets such as `7d` or `2w` are also accepted. Filtering uses
the start time, or the completion time when the start is missing.

Repeat `--log` to combine plain, gzip and bzip2 files. Overlapping records are
deduplicated and timestamp ordered. NUL-padded recoverable records and malformed
recognized records are counted in the report. Read, decompression and oversized
line failures return an error without a partial report. Missing starts retain
the successful event with unknown duration. Interrupted attempts never become
successful timing samples. An unfinished log entry does not prove a process
is still running; inspect the live process or the active Arise display instead.
Portage logs have no per-process identity, so genuinely concurrent independent
package-manager invocations cannot always be distinguished from these records.
New Arise removals emit standard Portage unmerge records for external readers;
the history command also accepts records emitted by older Arise versions.

`--json` emits the [arise.history.v1 schema](schemas/history-v1.schema.json).
JSON and automatically redirected output are uncolored. Text honors the global
`--color=auto|y|n` policy. Exit codes are 0 for a successful query (including no
matches), 1 for read/output failures, and 2 for invalid arguments.

## Estimates

`--show-estimates` is opt-in. It prefers successful timings for the same CPV,
then falls back to the same package. It excludes known different roots and
repositories, incomplete attempts, zero durations and durations of seven days
or longer. Repository identity is unknown in many existing Portage records.
Binary-install estimates are unavailable rather than borrowing source-build
durations. These logs do not reliably record USE, toolchain, hardware or cache
compatibility; the report discloses that limitation.

The plan shows total package work separately from modeled elapsed time, with
history coverage and the dependency chain length. The model follows the exact
planned prerequisite graph and an ordered queue with up to the configured job
count. Unknown tasks contribute no invented duration and make the estimate
partial. Load, temporary-space admission, serialized merges and contention can
make actual execution longer than the model; removal time is not included.

During execution, the display updates elapsed time, active jobs, historical
coverage, estimated remaining time and approximate finish time. Active-package
elapsed time uses Go's monotonic clock. When a package exceeds its estimate,
remaining time becomes unknown rather than reporting a negative value or an
unjustified zero. Interactive terminals refresh every five seconds; redirected
logs update on actual package events and do not receive timer spam.

Estimates are advisory. They do not change package selection, approval,
scheduling, journals or resume state. Native configuration-aware timing history
and richer uncertainty modeling remain in the
[estimation plan](planning/BUILD_TIME_ESTIMATION_PLAN.md).

The feature takes workflow inspiration from the
[genlop manual](https://github.com/gentoo-perl/genlop/blob/master/genlop.1) and
[qlop](https://github.com/gentoo/portage-utils/blob/master/qlop.c).
