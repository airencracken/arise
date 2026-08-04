# Build-time estimation plan

## Goal

Make Arise's time estimates explainable, useful during parallel execution, and
honest about uncertainty. Preserve the current median-based estimator as the
initial baseline, but stop presenting a single historical duration as though it
were a precise live ETA.

This work implements the build-history direction recorded in `PUNCHLIST.md`.
It does not make estimates part of plan authorization and must not affect
package selection, scheduling correctness, transaction safety, or recovery.

## Current behavior

Arise reads completed `>>> emerge` and `::: completed emerge` pairs from
`/var/log/emerge.log`, groups samples by category/package across versions,
discards non-positive and seven-day-or-longer durations, and selects the middle
sorted sample. The package estimate is fixed when execution starts. The plan
total is the sum of package medians, so it does not model dependency ordering
or `--jobs` parallelism.

The Firefox continuation on 2026-07-24 made the limitations visible:

- Arise and `qlop` produced roughly compatible median-based predictions;
- `genlop` produced a substantially shorter prediction from a different
  history/averaging method;
- none of the tools explained sample count, spread, compatibility, or why its
  estimate differed; and
- a summed plan duration would misrepresent a two-job dependency graph.

## Delivery stages

### 1. Make the baseline observable

- Return an estimate record rather than a bare duration: selected duration,
  sample count, minimum, median, p75, p90, maximum, and newest sample time.
- Define even-sized median behavior explicitly instead of selecting the upper
  middle value accidentally.
- Add `--estimate-details` output for package and plan views.
- Label the estimator and source, for example `historical median, 5 samples`.
- Report action coverage and packages without compatible history.
- Keep ordinary output compact; detailed statistics remain opt-in or JSON.

### 2. Establish authoritative history

- Append one versioned JSONL record per attempted package action.
- Record CPV, slot, repository, action, start/end/duration, success or failure,
  machine/build-profile fingerprint, jobs/load settings, relevant build flags,
  plan identity, and durable log/journal references.
- Lock appends, sync completed records, tolerate a truncated final record, and
  retain failed or interrupted attempts for diagnostics.
- Use only successful compatible samples for default predictions.
- Treat indexes and percentile summaries as disposable data reconstructable
  from the JSONL source.
- Continue reading `/var/log/emerge.log` as migration/fallback history, clearly
  marking those samples as having incomplete compatibility metadata.

### 3. Improve package prediction

- Key compatibility at least by category/package, slot, machine fingerprint,
  build profile, compiler/toolchain family, material USE/build-flag changes,
  and configured parallelism.
- Prefer exact compatible samples, then use explicit and reported fallback
  tiers when exact history is sparse.
- Weight recent samples without allowing a single recent outlier to dominate.
- Use a robust point estimate such as the median and expose p75/p90 as the
  uncertainty range.
- Define behavior for version changes, reinstalls, cold versus warm caches, and
  packages with no history.
- Never learn a successful-duration sample from failed, cancelled, or
  interrupted attempts.

### 4. Add live package ETA

- Track monotonic elapsed time from the actual action-start event.
- Display elapsed time and a decreasing estimated remainder.
- Do not allow the displayed remainder to become negative; switch to
  `over estimate by ...` once elapsed exceeds the point estimate.
- Show a range when enough samples exist, rather than false second-level
  precision.
- Rate-limit terminal updates and keep non-interactive output deterministic.
- Emit machine-readable progress events containing elapsed, point estimate,
  range, sample count, and estimator/fallback identity.

### 5. Estimate parallel transaction makespan

- Use the resolved dependency DAG, scheduler constraints, and configured job
  count instead of summing all package durations.
- Simulate the same dependency-ready scheduling policy used by execution.
- Account separately for parallel build work and serialized commit/lifecycle
  work where measurements permit.
- Recompute remaining makespan when an action starts, finishes, fails, or
  materially exceeds its prediction.
- Include running actions using their elapsed and predicted remaining time.
- Present both critical-path wall time and total package-work time so users can
  see why they differ.
- Mark the estimate partial when graph nodes lack compatible samples and show
  coverage by count and predicted work.

### 6. Add opt-in distributed timing reports

- Make reporting explicit opt-in. Building, installing, querying estimates,
  and using the package farm must not silently enable telemetry.
- Report one versioned observation per attempted package action with CPV, slot,
  repository, USE/build-state digest, outcome, effective jobs, and monotonic
  phase durations for fetch, unpack, compile, test, image install, validation,
  and commit where those boundaries are available.
- Describe hardware using estimation-relevant coarse features: architecture,
  ABI, logical core count, memory class, storage class, virtualization class,
  and a reproducible performance calibration score. Do not upload raw CPU
  identifiers when a coarse capability or calibration bucket is sufficient.
- Include compiler/toolchain identity, source-versus-binary mode, cache warmth,
  and material build features such as LTO, PGO, tests, documentation, and
  enabled language frontends.
- Never report hostnames, IP addresses, usernames, raw filesystem paths,
  environment contents, package configuration contents, or unrelated system
  inventory. Publish and schema-test an allowlist rather than maintaining a
  denylist of sensitive fields.
- Sign reports with a locally generated reporting identity, rotate or reset it
  on request, and ensure it is not also a package-farm account or machine
  identity. Treat every submitted observation as untrusted input.
- Rate-limit, size-bound, schema-validate, deduplicate by observation/build
  identity, and reject impossible durations and resource values server-side.
- Retain failed and interrupted observations for reliability analysis, but
  exclude them from successful-duration predictions.

### 7. Estimate from the package-farm corpus

- Query the complete stored timing corpus and weight observations by similarity
  rather than assigning clients to one rigid hardware bucket.
- Prefer observations matching architecture/ABI, CPV, USE-state digest,
  toolchain, jobs, memory class, storage class, virtualization class, and cache
  state. Reduce weight explicitly as compatibility diverges.
- Learn a client-specific scaling factor from packages that client has already
  built relative to comparable corpus medians. Apply that factor to sparse
  package histories with wider uncertainty instead of pretending the hardware
  is an exact match.
- Scale phase models independently: CPU calibration should influence compile
  work more strongly than network fetches or filesystem-heavy image/commit
  work. Memory pressure and swap observations must widen or invalidate a
  prediction rather than merely scaling CPU time.
- Fall back in a reported order: same CPV and similar hardware; same package
  and nearby versions; similar recipe/toolchain characteristics; calibrated
  client scaling; architecture-wide baseline; unavailable.
- Use robust weighted percentiles and outlier-resistant fitting. Return a
  point estimate, p50/p75/p90 or prediction interval, sample count, effective
  weighted sample size, compatibility tier, corpus age, and confidence.
- Compare local source build time, package-farm queue plus build time, artifact
  transfer/install time, and any immediately available compatible baseline
  artifact. Keep CPU-specific optimization differences visible in the
  tradeoff rather than treating all compatible artifacts as equivalent.
- Present decisions in user terms, for example `build locally: 38-55 min`,
  `wait for native farm artifact: about 8 min`, or `install generic artifact:
  under 1 min`. Estimates remain advisory and never select an artifact or
  authorize a transaction without the ordinary compatibility and approval
  checks.

### 8. Track longitudinal package cost and bloat

- Treat “bloat” as a vector, not a single accusation: source/distfile bytes,
  compressed binary-package bytes, installed exclusive bytes, shared bytes,
  file count, ELF/debug-symbol bytes, peak build-workspace bytes, peak memory,
  dependency-closure package count and bytes, total package-work time, and
  critical-path wall time.
- Record the recipe commit, CPV, slot, repository, profile, USE-state digest,
  toolchain, build features, splitdebug/strip/compression policy, architecture,
  ABI and cache state with every measurement. Never compare differently built
  artifacts without labeling the configuration delta.
- Separate package-owned installed bytes from the incremental dependency
  closure introduced on a frozen baseline. Attribute shared dependencies once
  and expose both exclusive and amortized views instead of double-counting.
- Track release-to-release and rolling-baseline deltas for selected major
  packages such as GCC, LLVM, Rust, Firefox, Chromium, LibreOffice and kernels,
  while allowing users to define their own watched set.
- Decompose growth by phase and component where evidence permits: added
  language frontends, generated sources, debug data, libraries, documentation,
  tests, localization, vendored dependencies and package-manager metadata.
- Store absolute values and normalized ratios such as installed bytes per
  compressed artifact byte, build minutes per installed MiB, and peak workspace
  per final artifact byte. Ratios supplement absolute cost; they do not replace
  it.
- Mark discontinuities caused by compiler, compression, profile, USE, ABI,
  debug, LTO, PGO or test-policy changes. Do not draw a package trend line
  through incompatible configurations.
- Show uncertainty and sample provenance for time/resource trends. Artifact
  sizes from reproducible farm builds may be exact, while client build time and
  peak-resource distributions require sample counts and percentiles.
- Alert only on configurable, sustained regressions against compatible
  baselines. A single release spike creates an investigation marker, not an
  automatic package judgment or transaction blocker.
- Keep longitudinal reports advisory and privacy-safe. Coarse hardware
  calibration is sufficient for time/resource normalization; artifact sizes
  do not justify collecting host identity or unrelated filesystem inventory.
- Publish machine-readable series and human dashboards that can answer both
  “why did this package grow?” and “what will this version cost on my machine?”
  from the same provenance-linked corpus.

## Validation

- Unit fixtures for odd/even medians, percentiles, corrupt/incomplete log
  records, overlapping CPV starts, failures, and compatibility fallback tiers.
- Deterministic DAG fixtures covering chains, independent work, fan-in,
  fan-out, mixed short/long jobs, and job-count changes.
- Property tests: predicted makespan is never below the longest mandatory
  dependency chain, never above serial total for the modeled stages, and does
  not increase merely because more identical worker slots are available.
- Replay historical world-update traces and compare predicted versus actual
  package duration and transaction makespan.
- Replay distributed observations across held-out hardware classes and package
  versions. Prevent observations from the evaluated client or build identity
  from leaking into its training set.
- Replay known package/profile transitions and verify that compatible
  release-to-release size/resource deltas are detected while profile, USE,
  toolchain, debug and compression changes create labeled discontinuities.
- Property-test closure accounting so shared dependency bytes are never counted
  more than once in one baseline comparison and exclusive contributions never
  exceed the measured final closure.
- Report median absolute percentage error, weighted absolute percentage error,
  p90 error, interval coverage, and bias; do not optimize only for one metric.
- Measure calibration and error separately for exact matches, similarity
  weighting, client-scaled fallback, architecture baseline, each build phase,
  and local-versus-farm tradeoff predictions.
- Compare the baseline, improved estimator, `qlop`, and `genlop` on the same
  frozen history without treating either external tool as authoritative.
- Add schema, property, fuzz, adversarial-input, replay, privacy-allowlist,
  duplicate-submission, signature, rate-limit, poisoning, and outlier tests for
  both the reporting client and ingestion service.
- Verify that disabling estimates produces no execution, plan, journal, or
  installed-state difference.
- Verify that telemetry disabled means no report is queued, written, or sent,
  including failure, retry, offline, package-farm, and upgrade paths.

## Initial acceptance criteria

- Every displayed estimate identifies its method and sample count.
- Live package output distinguishes elapsed, estimated remaining, and
  over-estimate time.
- Parallel plan ETA is DAG/job-aware rather than a serial sum.
- JSON output exposes point estimate, uncertainty range, coverage, and
  compatibility tier.
- Sparse or incompatible history yields an explicit `unavailable` or fallback
  label, not an unlabeled guess.
- Distributed estimates identify their corpus, compatibility tier, effective
  sample size, uncertainty, client calibration status, and observation age.
- Users can compare local source build, queued farm build, and available binary
  installation time without enabling timing reports.
- Timing reports are opt-in, allowlist-only, locally inspectable before upload,
  and removable from the local queue.
- Estimate history remains advisory and cannot invalidate or authorize a
  mutation plan.
- Longitudinal reports separate exclusive package cost, shared dependency
  closure and build-resource cost, and never compare incompatible build
  configurations as one continuous trend.
