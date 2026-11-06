# Concurrent package state checks

A world update stopped before merging ffmpeg because another installer had
committed comfyware applications while ffmpeg was building. The operation lock
correctly serialized commits, but the additional whole-state fingerprint check
rejected every intervening VDB or world change. It did not distinguish unrelated
applications from changes that could invalidate the prepared image or plan.

The corrected boundary retains the operation lock and initial state-bound
approval. It revalidates changed installed state against the remaining frozen
actions, rather than requiring the complete database to remain unchanged.

## Acceptance rules

- Wait for the existing Portage-compatible VDB lock before reading current
  installed metadata or merging a prepared image.
- Freeze current VDB dependency, USE and subslot bindings and independently
  validate the remaining approved actions against that state. A newly installed
  reverse consumer or blocker participates in this check.
- Remove completed actions, their prerequisite references and their selected
  decision records from replay. Keep the original approved evidence unchanged.
- Protect pending packages and the dependency inputs of potentially prepared
  images, including runtime dependencies of build tools. Changes to those
  installed records still require resolving and rebuilding the remaining work.
- Keep policy, command configuration and selected recipe/eclass bindings strict.
  Do not change package choices, add actions or relax constraints during replay.
- Compare state again after validation, then adopt the new baseline only if the
  proof succeeded and package state remained stable. Ignore unrelated world
  bookkeeping when deciding whether another package merge needs revalidation.
- Advance the baseline for Arise's own committed changes while its operation
  lock remains held. Incomplete changed VDB records are not silently adopted.

The full canonical authorization fingerprint still includes world and VDB
contents. Additional per-entry digests let the live guard identify changed
package inputs without hashing VDB payload bytes a third time. VDB root
symlinks are resolved before scanning and fingerprinting.

## Concurrent Arise checkpoints

Independent Arise commands also shared a default resume filename. A second
command could replace the first command's checkpoint, then prevent the first
from recording its next commit. A separate regression reproduces that failure
against the unmodified 0.0.50 source.

A transaction now reserves its checkpoint with a process-local ownership map
and a process-lifetime file lease. The lease remains active through final world
bookkeeping. Another new transaction receives a separate checkpoint and prints
its path for `--resume-file`. Successful private checkpoints are retired;
interrupted ones remain available. Resuming an active checkpoint is refused
without changing its saved progress, and process death releases its lease.
Resume requests bind the checkpoint snapshot read during planning and compare it
again under the lease. Completed and no-op resume bookkeeping uses the same
ownership check, so these paths cannot retire another active transaction's
checkpoint or overwrite progress that advanced during resolution.

Older Arise processes do not participate in this lease protocol. Finish or
resume existing old-version transactions before relying on concurrent Arise
checkpoint isolation.

## Regression evidence and limits

The state-guard regression reproduces a prepared ffmpeg update interrupted by
an unrelated app install and world selection. The original guard rejects it;
the corrected guard accepts it. Cases also cover unrelated removals, derived
VDB caches, dependency USE/rebuild/removal changes, selected sources, policy,
command environment, new reverse constraints/blockers/ABI consumers, incomplete
metadata, completed prerequisites and symlinked VDB roots.

The process integration uses the actual executor and operation locks, with a
second installer process committing a temporary VDB record and updating world
after releasing its lock. Compatible additions proceed; conflicting additions
leave the package pending and retain the checkpoint. Tests exercise serial and
parallel executor settings. Checkpoint cases cover concurrent executors in one
process, ownership during final bookkeeping, active-resume rejection and
subprocess death. All package writes stay in disposable test directories.

Evidence is retained in `.validation/mutation-state-baseline/` in the primary
checkout. This is not a live host world-update acceptance run. Dependency or
policy changes that need a different plan still stop with resume guidance; the
implementation does not automatically replace approved actions or reuse an
image whose dependency inputs changed.
