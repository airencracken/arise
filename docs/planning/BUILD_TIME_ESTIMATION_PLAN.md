# Build estimates

The [history command and opt-in ETA](../history.md) implement Portage-log
reports, timing statistics, exact-version/package median fallbacks, partial
coverage, and an ordered dependency-graph simulation. The native history,
configuration compatibility, richer uncertainty, serialized-phase models and
distributed corpus below remain deferred design. Estimates remain independent
of package selection and approval.

## Local estimates

Expose method, sample count, compatible history, median, p75/p90, range and
coverage. Define even-sized medians. Use successful compatible observations
only; keep failed/interrupted attempts for diagnosis. Versioned, locked, synced
JSONL history is authoritative; indexes are rebuildable and a truncated final
record is recoverable. Portage logs remain an explicitly incomplete fallback.

Compatibility includes package/slot, machine, toolchain, material USE/build
flags, jobs and cache state. Live progress uses monotonic elapsed time,
nonnegative remainder and explicit over-estimate state. Parallel ETA follows
the dependency DAG, ready policy and serialized commit lane; report critical
path separately from total package work. Missing history makes estimates partial.

## Optional distributed corpus

Reporting requires explicit opt-in and a schema allowlist. Never report
hostnames, IP addresses, usernames, raw paths, environment or configuration.
Use coarse hardware/calibration, architecture/ABI, recipe/USE/toolchain identity,
phase durations, jobs, cache state and outcomes. Authenticate, size-bound,
rate-limit, deduplicate and validate untrusted reports; keep reporting identity
separate from farm accounts. Failure observations never train success predictions.

Use a client-specific scaling factor with broader uncertainty for sparse local
history. Scale phase models independently. Report effective weighted sample
size, confidence, corpus age and fallback tier: same CPV and similar hardware;
same package and nearby versions; calibrated client scaling; architecture-wide
baseline; unavailable.

Compare local source build time, package-farm queue plus build time and transfer/
install time. Estimates remain advisory and never choose artifacts or authorize
transactions.

Treat “bloat” as a vector: artifact bytes, installed exclusive bytes, shared
bytes, files, peak workspace/memory, dependency-closure package count and bytes,
total work and critical-path time. Count shared dependencies once. Record
release-to-release and rolling-baseline deltas with labeled discontinuities;
never compare incompatible build configurations without disclosing the change.

## Acceptance

Test statistics, malformed/truncated records, fallbacks and deterministic DAGs.
Makespan cannot undercut a mandatory chain, exceed modeled serial work or grow
just from adding identical workers. Replay held-out hardware/package histories;
report bias, median/p90 error and interval coverage. Test allowlists, signatures,
deduplication, poisoning and shared-byte accounting. Verify telemetry disabled
means no report is queued, written, or sent. Disabling estimates must leave plans,
execution, journals and installed state identical.
