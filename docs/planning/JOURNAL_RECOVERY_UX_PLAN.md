# Journal and Recovery UX Plan

## Purpose

Arise's journal already provides durable rollback, but its operator interface
currently exposes the storage model too directly. A normal `recover status`
prints every historical operation as a flat list. On a machine with hundreds
of successful transactions, the one active operation is buried in output that
is useful to programs and forensic analysis but not to a person repairing an
interrupted package run.

The recovery interface must make the safe next action obvious without hiding
the underlying evidence or weakening fail-closed behavior.

## Human questions the interface must answer

The default output should answer, in this order:

1. Is recovery currently required?
2. Which package and phase were interrupted?
3. What filesystem root and how many paths are affected?
4. Is rollback automatic, recommended, unavailable, or already complete?
5. What exact command should the operator run next?
6. How can the operator inspect the full evidence before acting?

If no active operation exists, the first line should say so. Historical
committed and rolled-back operations should be summarized as counts rather
than printed individually.

## Proposed command surface

### `arise recover status`

Default, concise, actionable view. Show active or recovery-failed operations
in newest-first order, followed by aggregate historical counts.

```text
Recovery required: 1 interrupted operation

  operation-3665465613
  Package: sys-kernel/gentoo-sources-7.1.3:7.1.3::gentoo
  Stage: merging package
  Root: /
  Captured paths: 8,119
  Started: 2026-07-22 03:31:04 PDT (4 minutes ago)

Recommended action:
  arise recover rollback operation-3665465613

Inspect first:
  arise recover show operation-3665465613

History: 312 committed, 4 rolled back
Use `arise recover status --all` to list historical operations.
```

When healthy, print only:

```text
No recovery required.
History: 313 committed, 5 rolled back
```

An active journal is not journal corruption. Status should distinguish the two,
while mutating commands continue to recover or refuse according to transaction
policy.

### `arise recover status --all`

Show the complete historical table, newest first. Use aligned columns on a
terminal and no ANSI escapes when redirected. Include operation, time, package,
status, entry count and root. Support `--status=active`, `committed`,
`rolled-back`, and `failed` filters.

### `arise recover show <operation>`

Show one operation in detail:

- package CPV, slot and repository;
- requested operation (install, replace, remove, world mutation);
- stage reached and last durable event;
- start, update, commit or rollback timestamps and elapsed duration;
- root, VDB path, plan digest, Arise version and process ID;
- entry counts grouped by preimage kind and top-level path;
- backup bytes consumed and journal directory;
- terminal error or interruption signal when known;
- recovery eligibility and the exact safe command.

Do not print thousands of captured paths by default. Add `--paths`, with an
optional `--limit`, and retain `--json` for complete structured inspection.

### `arise recover rollback <operation>`

Before mutation, print a compact description of the exact active operation
being rolled back. Refuse committed and already rolled-back journals with a
clear explanation. On success, report package identity, restored path count,
elapsed time, final state, and the next useful command—normally rerunning
preflight so the plan reflects installed progress.

`--all-active` remains available for startup automation and exceptional
multi-journal recovery, but interactive use should enumerate the selected
operations before acting. It must never include committed journals.

### Machine-readable output

`--json` remains stable, versioned and complete. Human filtering flags must
also filter JSON results. Add a top-level schema version and separate
`actionable` operations from aggregate `history` so consumers do not have to
reimplement recovery policy. ANSI color, relative times and suggested shell
formatting remain presentation-only.

## Journal metadata required

The current summary records only operation ID, path, status, root and entry
count. Extend the versioned state with optional metadata so old journals remain
readable:

- package identity: category, PF/CPV, slot, repository;
- operation kind and lifecycle stage;
- UTC start, last-update and completion timestamps;
- plan SHA-256 and invocation/run identifier;
- Arise version, PID and boot ID where available;
- terminal error, signal or recovery reason;
- backup byte count and entry-kind counts.

Update stage and error metadata at durable transaction boundaries, not for
every low-level journal entry. Metadata writes must follow the same atomic,
fsynced state-transition rules as status changes and must not reintroduce the
former one-`fsync`-per-event performance problem.

When reading an older journal without these fields, display `unknown` or omit
the row instead of guessing package identity from captured paths.

## Retention and disk use

Committed history is evidence, but retaining every full preimage forever is
not a viable default. Implement explicit, conservative retention:

- never prune active, failed, malformed or recovery-incomplete journals;
- keep rolled-back journals longer than committed journals;
- retain compact transaction metadata after eligible backup payloads expire;
- prune only after a durable committed/rolled-back state and directory sync;
- support limits by age, count and total bytes, with safe defaults;
- provide `arise journal usage` and `arise journal prune --pretend`;
- make destructive pruning explicit and report reclaimed space;
- allow a configured policy and a `keep` marker for forensic cases.

Normal execution should warn before journal storage can exhaust the filesystem.
Space checks must account for active package work directories and journal
backups rather than treating them as unrelated consumers.

## Failure and corruption handling

- Distinguish `active`, `committed`, `rolled-back`, `rollback-failed`, and
  malformed/unreadable journals in human and JSON output.
- Never let one malformed historical journal hide a separate active journal;
  enumerate what can be read, report the damaged path, and exit nonzero.
- A failed rollback must remain actionable and preserve its backups.
- Explain lock contention with the owning PID/command when available.
- Refuse path traversal and journals outside the configured journal root.
- Never suggest manual deletion as the first recovery action.
- Report automatic startup recovery in the package log and terminal output.

## Presentation rules

- Color only on a terminal and honor `NO_COLOR` and configured color policy.
- Use color as emphasis, never as the only status indicator.
- Keep the healthy default to two lines or fewer.
- Put the recommended command after the diagnosis.
- Use thousands separators for large entry and byte counts.
- Show absolute timestamps plus relative age on terminals.
- Keep full paths and IDs copyable when they are command operands.
- Send warnings/errors to stderr and list/structured output to stdout.

## Delivery sequence

1. Replace the flat default with active-first output and history counts; add
   `status --all` and formatter tests.
2. Add optional metadata and populate it from merge/unmerge callers.
3. Add `recover show`, filters, path summaries and versioned JSON.
4. Improve rollback guidance and automatic-recovery messages.
5. Add usage accounting, conservative retention and pretend pruning.
6. Add corruption, old-schema, process-death, redirected-output, color and
   large-history acceptance tests.

## Acceptance criteria

- With hundreds of historical journals and one active journal, the active
  operation and rollback command appear within the first terminal screen.
- With no active journal, status clearly reports that no recovery is required
  without listing history row by row.
- An interrupted package is identified by package and stage without inspecting
  raw JSONL entries.
- Old journal versions remain recoverable and render safely with partial data.
- `--json` contains everything bug-report tooling and external helpers need.
- Retention can reclaim committed backups but never the only evidence needed
  to finish or diagnose recovery.
- Human presentation adds no per-entry durability sync to the merge hot path.
