# P3/P4/P6 checkpoint — 2026-07-18

> Historical checkpoint. It was the consolidated state on 2026-07-18 and was
> superseded by later execution-parity audits and the 2026-07-24 Portage
> self-hosting milestone. `PUNCHLIST.md` is the authoritative live gate ledger.

## Demonstrated now

- Resolver execution has a structured five-minute default deadline; `0`
  disables it without changing the independent backtrack ceiling.
- Cancellation is checked through search, complete-graph, repair, replay and
  speculation. Timeout results are non-executable and structured.
- Captured `@system`, explicit-package and normal `@world` cases finished
  verified during this development cycle. A later same-day deep/newuse
  `@system` rerun after repository/configuration drift produced 159 Arise versus
  143 Portage actions and 16 normalized differences, so that live case is open
  again and no new timing is published. Backtrack ceilings of 20 and 10,000
  produced identical normalized output and actual backtrack use in the covered
  stable capture.
- The former empty-tree allocation pathology was reduced from roughly 219
  seconds and 85 GB of cumulative allocation to a bounded explained result in
  the development case. This is a regression result, not yet a published
  Portage-equivalent performance claim.
- Typed phase policy, durable per-package logs, elog routing and the declared
  EAPI 7/8 phase/helper subset have portable coverage. Unsupported requested
  behavior fails before worker startup.
- Core logging is near closure: ordinary/split/compressed paths, canonical
  `T/build.log`, `PORTAGE_LOG_FILE`, ordered records, parallel isolation,
  filtering, elog sinks, fail-closed finalization and interrupted-log discovery
  are implemented. Production action-executor hookup, exact Portage formatting
  differentials, cancellation/signal records and remaining boundary injection
  are still open.
- Transactional merge and unmerge use a durable path-confined journal and the
  Portage VDB lock. `arise recover status`, targeted rollback and
  `--all-active` rollback expose deterministic recovery to administrators.
- A serial action executor now preflights every selected action before starting
  the first worker, persists resume completion only after transaction commit,
  and connects verified source actions to the phase protocol, durable log and
  journaled merge. The narrowly declared first-install lane can target live `/`
  only for one exact additive source action with absent file/VDB targets and no
  custom package lifecycle hooks.
- Mutation approval is bound to a canonical verified-plan SHA-256 containing a
  fresh VDB/world/config/selected-recipe/eclass fingerprint and requires both
  `--experimental-live-mutation` and `--approve-plan-sha256`. State is
  fingerprinted again immediately before execution and drift fails closed.
- Replacement removes obsolete exclusively-owned payload, preserves shared
  ownership, and restores old payload/VDB state after injected failures.
- Replacement transaction callbacks cover old `pkg_prerm`/`pkg_postrm` and new
  `pkg_postinst` ordering plus rollback after post-removal failure. Production
  worker integration and journaling of arbitrary lifecycle ROOT writes remain.
- Core `CONFIG_PROTECT`/`CONFIG_PROTECT_MASK`, root-relative `CONTENTS`, native
  `NEEDED`/`NEEDED.ELF.2`, `BUILD_TIME`, global/package `COUNTER`, exact ebuild,
  dependency metadata and native `environment.bz2` publication are covered in
  disposable roots.
- Merge preserves regular-file and newly-created-directory modes, uid/gid and
  mtimes, symlink ownership/no-follow mtimes, hardlink inode identity and Linux
  xattrs. Safe journaled type transitions are covered; replacement of a
  non-empty local directory fails closed.
- CLI deselection uses a locked, mode-preserving, fsynced atomic world update.
- The real `media-sound/apulse-0.1.14` ebuild now completes its Manifest-
  verified EAPI 8 `cmake-multilib` lifecycle against a disposable target ROOT,
  using separately modelled ROOT, SYSROOT and BROOT, and produces its wrapper,
  libraries, VDB record, journal and durable package log. `pkg_postinst` must
  succeed before transaction commit.
- The first live mutation completed successfully on 2026-07-18: the frozen
  state-bound one-action apulse plan built from source and committed 47
  journaled live-root preimages. Host-side verification confirmed root-owned
  wrapper/libraries/VDB, complete CONTENTS and expected ELF dependencies; the
  journal is committed and no active recovery operation remains.
- Journal schema v2 additionally preserves UID/GID, atime/mtime, full modes,
  symlink targets and xattrs for replacement rollback. The next canary lane is
  an exact same-version apulse reinstall, restricted to paths owned by its old
  CONTENTS and the same lifecycle-free closure.
- The exact live apulse reinstall subsequently passed: the resolver emitted one
  `reinstall` action, a second 47-entry journal committed, CONTENTS/VDB were
  regenerated and linkage remained intact.
- The next genuine upgrade candidate is `x11-apps/xrandr-1.5.3` to `1.5.4`.
  Its one-action plan and real `xorg-meson` disposable upgrade pass, including
  old-VDB removal. The old xorg-3 post hooks are classified no-op for this
  non-font package; selected ebuild/eclass sources remain state-hash bound.
- That exact xrandr live upgrade passed: one `update` action committed a
  78-entry replacement journal, removed the 1.5.3 VDB, installed a working
  1.5.4 binary and published complete CONTENTS/ELF metadata.
- Deselect now has a read-only JSON plan, world-state digest, exact approval
  gate and locked state revalidation. `--pretend` no longer mutates world.
- The live bind-tools deselect passed with an exact one-line world delta. Its
  empty-payload dummy VDB now has a whole-state-verified standalone removal
  plan and an explicit live-root journal path; removal remains a separate
  approved action.
- The subsequent standalone removal passed whole-state verification and
  committed a 28-entry live-root journal; the bind-tools VDB is absent. Its
  dependency `net-dns/bind-9.18.42` already owns `/usr/bin/dig`, so the
  deprecation workflow now uses a state-only `select` plan (the semantic
  equivalent of `emerge --noreplace`) to add bind to world without rebuilding.
- The approved state-only bind selection passed with an exact one-line world
  delta and no package rebuild. A subsequent live `dig +short google.com`
  query returned addresses, functionally confirming the replacement DNS client
  after bind-tools removal. Exact parsed VDB identities now guard selection so
  a hyphenated sibling such as `bind-tools` cannot impersonate `bind`.

## Still gated

- General live-root mutation remains disabled; only the explicitly authorized
  additive single-package canary subset is unlocked.
- The executor is not promoted to production live-root use: it is wired only
  for explicitly authorized disposable roots (plus Manifest-verified
  source fetch-only).
- P3 still needs portable damaged-world closure, remaining semantic
  differentials, repeated determinism/mutation gates and equivalent-outcome
  performance publication for the expanded command matrix.
- Signal Desktop is retained as a binary/user-patch regression, not used as a
  representative benchmark. The resolver matrix uses explicit packages,
  `@system`, normal and damaged `@world`, preserved rebuild and empty-tree cases.
- P4 still needs full supported helper/default-phase closure, environment and
  directory differentials, replacement lifecycle ordering and broad failure
  injection. Representative package-family breadth belongs to P4R; the Go
  overlay supply chain belongs to P4G.
- Lifecycle ordering is inside the merge boundary, but lifecycle phases can
  mutate arbitrary ROOT paths. Live promotion waits until those writes are
  observable and recoverable by the journal.
- P6 still needs preserve-libs, ownership/timestamp/hardlink/xattr/ACL/capability
  parity, world-addition coupling, exhaustive kill recovery and Portage image/VDB
  differentials.
- P8 now explicitly tracks canary eligibility, whole-plan preflight, recovery
  command/evidence capture, serial resume semantics and empty-tree boot-critical
  protections. These are live-promotion gates, not optional release polish.
- `probe-preserved-rebuild.sh` provides a root-only, read-only evidence capture
  for the otherwise protected preserved-libraries registry and Arise's native
  ELF/VDB scan. It checks every external harness prerequisite before use; the
  product scan itself does not invoke `ldd` or `readelf`.
- Structured live evidence exposed and fixed an over-selection bug: 335 general
  missing-SONAME edges (mostly package-private plugin libraries) had been mixed
  into `@preserved-rebuild`. That set now contains only consumers of paths in
  Portage's preserved-libraries registry; general linkage damage remains the
  separate revdep-rebuild operation.
- Preserved-rebuild output now identifies installed consumers with no ebuild and
  offers the exact pretend-only uninstall command needed to run whole-state
  removal verification. It never removes them implicitly: execution remains
  bound to the resulting state/plan SHA-256 and the explicit live-mutation
  approval gate.
- The first stale-consumer repair passed live: `dev-python/m2crypto-0.38.0`
  was absent from world and current repositories, passed whole-state removal
  verification, and was removed in committed 208-entry journal
  `operation-489531626`. The preserved set fell from 10 packages/49 edges to
  9 packages/47 edges. `net-wireless/crda-4.14` remains the next isolated
  orphan candidate; unavailable `dev-qt/qtcore-5.15.16` requires migration
  analysis and must not be presumed safely removable.
- The first crda removal attempt failed closed and rolled back before removing
  its VDB because this split-usr host uses the relative compatibility symlink
  `/lib/udev -> ../usr/lib/udev`. Journal confinement now resolves root-confined ancestor
  symlinks to canonical paths while still rejecting escapes, cycles and unsafe
  absolute links in disposable roots. Focused normal and race tests cover the
  corrected boundary; the crda plan must be regenerated because source and
  state-bound authorization changed.
- The regenerated crda removal then committed successfully as
  `operation-4111477352` with 58 entries on the split-usr host. The prior
  attempt remains durably marked rolled-back, crda's VDB is absent, and the
  preserved set fell to 8 packages/46 edges.
- QtCore analysis exposed a separate uninstall-verifier gap: dependency strings
  showed no reverse edge, but 13 installed Qt/PyQt packages record
  `libQt5Core.so.5` in `NEEDED.ELF.2`. Native reverse-ELF verification now
  blocks such removals and lists consumers. Unavailable preserved consumers
  with linkage users are classified `replacement-required` and receive no
  removal command. The installed Qt5 ABI island contains 13 Qt providers and
  one external ELF consumer, `dev-python/pyqt5-5.15.11`; script/dependency
  consumers still require analysis before any retirement plan.
- Arise now derives the transitive consumer-first reverse-ELF retirement
  closure and can feed it directly to one exact, whole-state-verified batch
  uninstall plan. The first Qt5 canary plan exposed a slot bug in repair
  eligibility: same-name Qt6 ebuilds for qtmultimedia, qtsvg and qtdeclarative
  incorrectly blocked retirement of installed slot 5. Repository availability
  is now matched to the installed slot using md5-cache metadata with a
  conservative ebuild fallback; a parallel-slot regression test covers it.
- The next live Qt plan exposed a removal-overlay identity bug: command-created
  actions omitted repository identity, so `::gentoo` installed nodes survived
  the verifier's removed map and continued contributing dependencies. Simple
  removals had passed accidentally when those retained dependencies remained
  satisfied. `VerifyTransaction` now requires exactly one installed
  version/repository/slot match and completes the canonical identity before
  overlay construction. The real 14-package Qt5 closure subsequently verifies
  with zero conflicts and explicit slot/repository identities for every action.
- The approved 14-package Qt5/PyQt5 retirement closure committed successfully
  with one state-bound plan and 14 consumer-first package journals. Every VDB
  target is absent, world is byte-identical, and preserved-rebuild fell from
  8 packages/46 edges to 7/30. This is the first live demonstration that Arise
  can derive and execute a damaged-state closure that would otherwise require
  manually widening `--oneshot --nodeps` batches.
- On the repaired state, a deep/newuse `@system` run with build dependencies
  and backtrack 20 completed in 2.13 seconds without backtracking. It proposed
  158 actions but remained non-executable due to 13 final-state conflicts:
  twelve stale/misaligned Python target edges and one Perl any-of. Resume-time
  diagnosis found that this invocation accidentally omitted the required
  `--complete-graph` option. Three corrected reruns each produced the same
  normalized 159-action plan with zero conflicts and zero backtracks, and
  whole-state verification repaired the retained dependency graph in two
  passes. The normalized failed and corrected summaries are stored in
  `docs/evidence/P3_SYSTEM_POST_REPAIR_2026-07-19.json`.
- The subsequent Portage atom/EAPI contract expansion retained the corrected
  live result. Two final post-contract runs were byte-for-byte plan-identical:
  159 actions, no uninstalls, conflicts or backtracks, and direct plan SHA-256
  `8b63f31e027b7e148dacfca45185541789eb1e6f29d3ea1aadcef7a583f51c33`.
  Correct subslot enforcement initially exposed an installed-identity leak in
  blocker replacement selection; the qtbase/qt5compat regression is now fixed
  and permanently covered.

## Live-operation acceptance gates

New install, reinstall, installed-package upgrade, deselection, standalone
removal and state-only selection have passed explicitly approved live runs.
Promotion beyond the single-action lane still requires `@system`, `@world`,
preserved-rebuild and empty-tree update gates.

## Validation

The current tree passes the full Go suite, focused race suites, Bash syntax
checks for maintained scripts and `git diff --check`. Host-dependent differential
lanes remain separate and must record exact configuration and repository state.
