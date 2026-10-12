# World-update correctness and performance audit — 2026-10-11

## Scope and outcome

The supplied `arise.log` reported 258.460 seconds and 20 exhausted backtracks
for `-pavuDN @world`, with a conflict against `perl-core/Test-Harness` even
though the transaction scheduled its removal. `emerge.log` resolved in
26.97 seconds with one backtrack.

The corrected world plan has 294 merge actions and one removal, zero
backtracks, and passes both resolver verification and independent plan-impact
validation. Three final verbose pretend runs finished in 5.29, 5.31 and
5.33 seconds wall time. Comparable emerge runs finished in 28.51, 28.63 and
28.38 seconds. This is a 5.4-fold wall-time improvement relative to emerge
on these inputs, not a claim about every package or machine.

## Confirmed defects and fixes

| Defect | Resulting behavior |
|---|---|
| Complete-graph rebuilds re-added a blocked package already scheduled for removal. | Both ordinary ABI transitions and stale-binding repair respect exact removal identities. Valid plans stop replaying unrelated choices. |
| Installed ABI scans copied version records and evaluated repository USE policy repeatedly. | Installed dependency inspection explicitly selects saved VDB metadata and installed USE without evaluating repository policy. |
| Scheduling a rebuild hid its old ABI binding from subsequent inspection. | Saved installed bindings remain available independently of replacement scheduling. |
| Slot-operator provider updates discarded the original dependency atom and root domain. | Provider selection preserves constraints, justification and BROOT/SYSROOT placement. The independent validator accepts the justified qpdf, LMDB and LuaJIT updates. |
| Transaction verification accepted unknown or ambiguous install identities and could repair a different overlay from the one it returned. | It validates exact identities, preserves caller inputs and certifies only the supplied transaction using retained VDB dependencies. |
| Removed disabled profile-fixed IUSE flags caused unnecessary `--newuse` rebuilds. | IUSE changes respect mask/force policy while enabled-state changes still trigger rebuilds. The unnecessary BusyBox rebuild disappears. |
| ABI repair labeled newer consumer versions as reinstalls. | Consumer upgrades use the update lifecycle and retain the replaced identity. |
| `--exclude` compared atoms against action-map keys after selection. | Exclusions participate in candidate selection, rebuild policy and blocker removals; an excluded required update remains an explained failure. |
| ABI propagation grouped all provider and consumer slots by package name. | New parallel slots do not invalidate retained slots; each changed provider slot and affected installed consumer instance is handled separately. Stale lower consumer slots are inspected, and BROOT actions do not invent ROOT rebuilds. Combined slot/subslot input is normalized. |
| Displaying download totals hashed cached source archives. | Display estimates inspect file type and size. Acquisition and explicit verification still check digests; a corrupt same-size archive can make actual downloads exceed the estimate. |

## Measurements

| Run | Resolution | Whole command | Backtracks | Outcome |
|---|---:|---:|---:|---|
| Supplied Arise log | 258.460 s | Not recorded | 20 | False blocker conflict |
| Reproduced unfixed source, CPU profiling enabled | 282.451 s | About 290 s | 20 | Same conflict |
| Final Arise, three verbose runs | 4.431–4.444 s | 5.29–5.33 s | 0 | Independently validated |
| Emerge, three equivalent verbose runs | 26.21–26.48 s | 28.38–28.63 s | 1 | Resolved |

The baseline profile attributed about 42% of sampled CPU time to policy-atom
matching. After removing the false conflict, repeated installed ABI inspection
was the main resolver hotspot. Profiling the verbose command separately exposed
another 7.24 seconds of sampled CPU time hashing archives for download totals.
A focused 64 MiB cached-archive display benchmark fell from about 40.5 ms to
11–12 microseconds per estimate.

The index, VDB, world file and user configuration were copied for Arise runs.
Repository manifests and active profile paths still referenced the same live
repository trees. The captured independent fixture and plan freeze validation
inputs. These are host-state measurements, not clean-root installation results.
Emerge's `news` feature was disabled for timed comparisons because its
post-pretend news ownership update was blocked by the read-only sandbox; this
does not change dependency resolution.

## Action differences and adversarial validation

The contradictory Test-Harness reinstall, unnecessary Rust slot and BusyBox
rebuild disappear. Download totals agree with emerge at 1,460,470 KiB.

Four additional merge actions remain: `perl-core/Getopt-Long`,
`virtual/perl-CPAN-Meta-Requirements`, `virtual/perl-Module-Load`, and
`virtual/perl-parent`. Removing each action from the frozen plan produces an
unsatisfied retained dependency or ABI binding. Removing the Test-Harness
uninstall produces a blocker violation. Duplicating a merge action is rejected.
These checks justify the additional actions under the retained-installed-state
contract; they do not assert exact action-set equivalence with Portage.

Independent impact validation recorded 71 pre-existing host violations and no
new violations. It does not assert that the original installed state was clean.

## Verification and limits

- Root-cause regressions reproduced the failures before their fixes; additional
  cases cover version/repository/root identities, parallel slots, lifecycle
  classification, exclusion failures and USE-policy boundaries.
- `make test vet` passed, including worker shell syntax and the full Go suite.
- Race tests passed for resolver, distfiles, plan adapter, independent validator
  and Portage policy packages.
- Live-Portage test packages compiled; live package execution was not run.
- The complexity ratchet passed at average 7.86, with 18 functions over 50.
- Cache-estimate tests cover partial files, same-size corruption, symlinks,
  deduplication and unsuccessful-request atomicity; strict verification remains
  covered by the existing acquisition and Manifest tests.

The broader command sweep found existing Qt slot conflicts in emerge's
`@system` plan. Arise's system plan passed independent validation. Both tools
refused the tested deep Nano update with conflicts. Those failed comparisons
are retained as diagnostic evidence, not counted as successful equivalence or
speed comparisons.

No packages were installed or removed, and no release was published. The audit
covers resolution, validation and pretend-command performance; it does not
establish source-build, merge-hook or recovery correctness for this world plan.

Private host fixtures, profiles, timing logs, benchmark results and the
mutation-check harness are retained outside Git in
`.validation/world-update-audit-20261011/`.
