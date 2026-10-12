# Measurement evidence

These JSON files preserve dated inputs, provenance and measurements used by
regression checks or published claims. They do not describe today's host or
certify a later revision.

- `RELEASE_0.0.7_PERFORMANCE_2026-07-29.json`: historical tool comparisons.
- `COVERAGE_BASELINE_*.json`: instrumented coverage snapshots.
- `HARDENED_STAGE3_WORLD_UPDATE_2026-08-04.json`: observed transaction, with
  explicit unknowns and limits; not the current fresh-stage3 acceptance gate.
- Other snapshots retain resolver, timing and post-repair observations.

Current methodology is in [performance results](../performance-results.md),
[test lanes](../testing/TEST_LANES.md) and the
[0.0.42 review](../reviews/2026-10-11-stabilization-release.md).
Superseded prose is available in Git history.
