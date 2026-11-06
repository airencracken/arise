# Shell-state contract audit

The repeated failures came from an incomplete model of the ebuild execution
environment. The earlier audits checked individual lost values and functions,
but did not adequately distinguish package state, worker implementation state,
and Bash runtime state. Portage's own release checks also did not exercise
Arise's rebuild driver, allowing Arise's self-upgrade failure through.

The concrete trigger is the published Arise 0.0.48 ebuild. It defines
`ARISE_COMMIT`, which the worker discarded with every `ARISE_*` name. Rebuilding
that exact ebuild with the old driver reproduces the reported blank `-commit`
argument and manifest verification failure in `src_prepare`.

## Contract and findings

The reference is PMS sections 11.2 and 12.3.17 in the
[EAPI 8 specification](https://projects.gentoo.org/pms/8/pms.html), the
[EAPI 9 specification](https://projects.gentoo.org/pms/9/pms.html), and the
installed Portage `save-ebuild-env.sh`, `filter-bash-environment.py`, and
`phase-functions.sh`. The reference runtime is Portage 3.0.82.2 with Bash 5.3.20.
The final comparisons used Gentoo repository commit
`7220ba41324651394e2cdabe993d0e8d18a57131`.

| Boundary | Confirmed problem | Repair and observable check |
| --- | --- | --- |
| Package variables | The `ARISE_*` filter discarded legitimate package data, including the source commit. `BASH*` also discarded completion settings. | Worker controls use the PMS-reserved `___ARISE_` namespace. A clean instance of the running Bash identifies its runtime variables; unrelated application prefixes remain package data. |
| Functions | The `arise_*` filter discarded package helpers. Generic implementation names such as `emit` could replace package functions. | Private worker functions use reserved names; package functions survive independently of their application prefix. |
| Eclass source scope | Ordinary locals in `inherit` could intercept assignments intended as eclass globals. | Locals surrounding foreign source execution use reserved names. A regression checks the resulting package globals in the next worker. |
| Install-helper state | Conceptual destinations and option arrays used unreserved global names. | Helper state is private and carried explicitly. V2 destinations, options, compression queues, strip queues, and inherited-eclass state are migrated. EAPI 7+ no longer receives the removed public destination variables from worker initialization. |
| Function attributes | Saving function bodies alone lost export and trace attributes. Child shells could not call exported package helpers. | Bodies and attributes are emitted separately. Child-shell execution is checked between source phases and from installed VDB state. Here-document contents are never edited to change attributes. |
| Readonly declarations | Arise retained readonly flags across workers, while Portage strips them and preserves the values. | Package declarations follow the observed Portage behavior. Manager-owned identity controls remain protected by the worker. |
| Runtime configuration | Ambient session, proxy, cache and XDG settings were retained as package state. Installed snapshots could also replace current locale and feature settings with build-time values. | Saving follows Portage's explicit runtime-variable exclusions. Loading installed state restores the current locale and feature settings, while source-phase changes retain their normal persistence. Application-specific locale names are not discarded by prefix guessing. |
| Older snapshots | Changing namespaces alone would lose old helper queues or retain old readonly restrictions. | Bash parses V2 state in a child shell and emits V3 before the hook runs. The original snapshot stays unchanged; temporary normalization files are removed. State that older versions never recorded cannot be recovered. |
| Snapshot framing | A configuration value may contain text resembling a format marker. | Current installed snapshots start with the V3 marker and validated lifecycle metadata before the typed configuration header. Quoted package data cannot select the legacy decoder for current snapshots. |
| Lifecycle discovery | Text scanning mistook `pkg_info ()` in a here-document for a real function. | V3 records carry a lifecycle list generated from Bash's actual function table. Unknown or duplicate phase identifiers fail validation. Package text is opaque to this metadata reader. |
| Request environment | Package configuration rejected all `ARISE_*` settings, while initial installed headers used the same broad exclusion. | Package-specific settings are accepted and retained; reserved worker controls and process-startup injection variables remain rejected. |
| Helper protocol migration | An older running process may call a newly installed binary as its version-query helper. | The helper accepts the preceding control names only when the new reserved controls are absent. Present but invalid reserved controls fail rather than falling back to application values. |
| Process descriptors | A background child of a package phase could inherit the event pipe and prevent a completed worker from reaching EOF. | Close the protocol descriptor around package and default phase calls. A regression reproduces the wait before the fix and completes promptly afterward. |
| Release verification | Portage built the package successfully while Arise could not build itself. | Release verification now also uses Arise's actual rebuild driver to build the staged, pinned source ebuild and install it into a disposable root. |

## Reference comparisons and regression evidence

The EAPI 7/8/9 Portage comparison checks actual installed fixture contents. Its
state matrix covers application-prefixed variables, ordinary and exported
variables, explicit unsets, indexed and associative arrays, quoted multiline
values, integers, case-converting variables, namerefs, readonly values, helper
creation/replacement/removal, exported function execution in a child Bash, and
phase identifier visibility. The expanded comparison failed before the
attribute repairs and matched afterward in all three EAPIs.
It also compares ambient runtime settings: stale XDG, user, proxy and cache
sentinels reproduced a second difference, then matched after the runtime-policy
repair. A separate installed-state regression checks current locale and feature
settings, including the distinction between known locale controls and unrelated
package variables.

The existing source-directory, missing-source, failed-snapshot, runtime-path,
QA-isolation, confinement, and removal-lifecycle regressions remain enabled.
The real completion QA regression and binutils-libs unpack/prepare check also
remain in the live lane.

The real self-host test requires setup, unpack, prepare, configure, compile,
test, install, pre-install and post-install using `RebuildPackage`, checks the
installed binary's version, and verifies the completion file and installed
environment. Running the same harness against the old production code
reproduces the user's exact manifest error. The corrected driver completes it.

The release tool records `self_host_verified` separately in its ledger. The
required live phase-contract lane runs before this canary; missing reference
prerequisites fail that release lane instead of silently skipping comparisons.
A failed self-host gate blocks publication; a prepared but unpublished ledger
created before this gate must pass verification again. Already completed
releases retain idempotent publication retries. Tests exercise both a failed
canary and a resumed ledger that lacks the evidence.

Evidence is retained under `.validation/package-variable-persistence/` and the
release's validation directory in the primary checkout.

## Rollout and limits

The execution ABI advances to 6: earlier Arise builds could silently lose
package inputs or exported helpers, rather than always failing before an
artifact was produced. ABI-5 packages are therefore also selected for repair.
Current-ABI packages and unmarked Portage packages retain their existing
selection behavior.

This audit covers state crossing eclass, phase, QA, artifact and installed-hook
boundaries. It does not certify every ebuild command, eclass, architecture, or
configuration against Portage. Aliases, traps, working directories, positional
parameters, and arbitrary shell-option changes are not represented as durable
package state by this snapshot format; phase entry directories and parser
options remain managed separately. The full host update and fresh-stage3,
interruption/resume, linkage, reboot, root-to-portage credential transition,
and full bubblewrap acceptance lanes remain separate checks.

V2 records retain their legacy phase-discovery compatibility fallback; only V3
contains the structured lifecycle manifest. Missing values or function attributes
that older versions never saved require rebuilding rather than inference from
the current repository.

Portage's special EAPI 9 `S` export for libtool remains an explicitly documented
implementation difference from Arise's PMS visibility checks. That field is
not used to claim byte-for-byte equality of the complete shell environment.
