# Continuation and retry

Proposed keep-going/retry semantics; not a claim of current CLI support.

Persist original goals, options, approved digest, stable action identity,
commit/failure/blocked state, attempts, cycle/state/error fingerprints and terminal
reason. Publish atomically and retain failed-run evidence under new generations.
Current resume saves progress and favorites; option-context replay remains open.

A failed branch blocks its dependents and rolls back uncommitted mutation.
Independent work may finish. Drain workers, recover journals, take a locked
snapshot and re-resolve original goals against committed state. Exit nonzero
when any original goal is unsatisfied. Never replay committed payload solely
because post-commit maintenance failed.

Continue automatically only for a proven subset of the approved plan: identical
version, slot/subslot, repository, USE, domain, merge/mutation kind and verified
inputs, with no new removal/replacement or weakened dependency order. Changed
solutions require a separately saved, explicitly approved continuation plan.
Prepared images need complete input/state equivalence before reuse.

Retry only classified transient errors, changed prerequisites/state or explicitly
requested actions. Unknown errors are permanent for the cycle. Enforce per-action
and cycle limits; stop on repeated no-progress fingerprints, policy failure,
unapproved changes or cancellation. Backoff is capped and context-cancellable.

Acceptance: multi-branch scheduling, commit/resume ordering, subset approval,
state/config drift, post-commit failures, cancellation and process death at every
persisted boundary. Report committed/failed/blocked/remaining counts and the exact
terminal reason without calling uncommitted work complete.
