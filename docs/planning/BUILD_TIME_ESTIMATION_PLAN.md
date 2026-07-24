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
- Report median absolute percentage error, weighted absolute percentage error,
  p90 error, interval coverage, and bias; do not optimize only for one metric.
- Compare the baseline, improved estimator, `qlop`, and `genlop` on the same
  frozen history without treating either external tool as authoritative.
- Verify that disabling estimates produces no execution, plan, journal, or
  installed-state difference.

## Initial acceptance criteria

- Every displayed estimate identifies its method and sample count.
- Live package output distinguishes elapsed, estimated remaining, and
  over-estimate time.
- Parallel plan ETA is DAG/job-aware rather than a serial sum.
- JSON output exposes point estimate, uncertainty range, coverage, and
  compatibility tier.
- Sparse or incompatible history yields an explicit `unavailable` or fallback
  label, not an unlabeled guess.
- Estimate history remains advisory and cannot invalidate or authorize a
  mutation plan.
