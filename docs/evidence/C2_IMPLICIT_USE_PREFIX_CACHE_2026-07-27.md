# C2 implicit USE_EXPAND prefix-cache optimization

## Verdict

Accepted as a separately authorized follow-up performance cycle. Caching
normalized `USE_EXPAND_IMPLICIT` prefixes once per resolver reduced median live
deep/newuse wall time by 8.5%, median combined CPU time by 10.2%, profiled total
allocation by 17.2%, and profiled retained heap by 27.5%.

The process-level median peak RSS increased 8.7% across these short, noisy
runs. The speed-first policy admits that tradeoff, and the heap profiles do not
show a structural memory regression.

## Baseline and workload

The baseline is the accepted shared fingerprint-buffer implementation recorded
in
[`C2_FINGERPRINT_BUFFER_2026-07-27.md`](C2_FINGERPRINT_BUFFER_2026-07-27.md).
The host, repository, profile, VDB, world set, cache policy, command shape and
measurement tool are unchanged.

Every baseline and candidate run:

- exited zero;
- completed whole-state verification;
- produced plan SHA-256
  `143d194ae78ba3a9b1a6f7517051be06087808345367ee7f37d43a1a081afef4`;
- produced state SHA-256
  `4c7a0233cce4d97ceec98ffc67c2fb65978b6cc4b87402a35691e8f67806c8e7`;
- retained identical normalized actions, conflicts and warnings.

## Diagnosis

After the fingerprint optimization, the CPU profile still attributed about
half a second cumulatively to effective USE policy and candidate USE
construction. `implicitUseExpandFlag` lowercased, trimmed and appended a
separator to every configured `USE_EXPAND_IMPLICIT` variable for every
candidate flag.

Those prefixes are immutable for one resolver. The accepted implementation
normalizes them once when resolver state is constructed. Direct test fixtures
retain lazy initialization so they exercise the same semantics.

## Repeated results

Times are seconds; peak RSS is KiB.

### Baseline

| run | wall | user | system | CPU total | peak RSS |
|---:|---:|---:|---:|---:|---:|
| 1 | 3.18 | 4.38 | 0.58 | 4.96 | 919,048 |
| 2 | 3.23 | 4.63 | 0.62 | 5.25 | 1,010,848 |
| 3 | 3.16 | 4.59 | 0.54 | 5.13 | 794,148 |
| 4 | 3.15 | 4.39 | 0.55 | 4.94 | 776,408 |
| 5 | 3.16 | 4.57 | 0.53 | 5.10 | 773,620 |
| median | 3.16 | | | 5.10 | 794,148 |

### Candidate

| run | wall | user | system | CPU total | peak RSS |
|---:|---:|---:|---:|---:|---:|
| 1 | 2.85 | 4.24 | 0.52 | 4.76 | 779,128 |
| 2 | 2.89 | 3.97 | 0.57 | 4.54 | 844,588 |
| 3 | 2.93 | 4.30 | 0.61 | 4.91 | 995,492 |
| 4 | 2.89 | 4.03 | 0.55 | 4.58 | 868,972 |
| 5 | 2.91 | 3.98 | 0.55 | 4.53 | 862,964 |
| median | 2.89 | | | 4.58 | 862,964 |

Profiled total allocation fell from 1,236.25 MiB to 1,024.17 MiB. Profiled
retained heap fell from 502.54 MiB to 364.53 MiB. These profiles indicate that
the higher process RSS median reflects garbage-collection timing and sampling
variance rather than additional retained resolver state.

## Tests

- unit test for normalized prefix construction;
- property comparison against the prior per-call semantics;
- mutation checks for separator boundaries and hostile prefixes;
- candidate USE and newuse compatibility tests;
- repeated live plan/state digest equivalence;
- full test, vet, race, adversarial, mutation-contract and formatting gates.

## Next profile owners

VDB metadata reads, dependency graph edge construction, atom parsing and
resolver snapshot decoding now lead allocation. Effective USE policy still has
measurable CPU cost, particularly repeated package-atom matching. A further
cycle should choose between compiled policy-rule indexes and a lower-allocation
dependency-edge representation only after a fresh profile.
