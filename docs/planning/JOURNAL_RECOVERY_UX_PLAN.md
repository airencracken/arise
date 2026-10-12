# Recovery presentation

Prioritize active and failed operations; summarize successful history. Identify
package, phase, ROOT, last durable event and exact next command. Keep full IDs
copyable and expose details/paths only on request. Old journals render unknown
metadata without guessing. Versioned JSON remains complete and uncolored.

Add optional package/plan/stage/time metadata at durable boundaries, without
per-entry synchronization. Status must distinguish interruption from corruption;
one malformed historical record cannot hide another active operation. Report
unreadable records and lock ownership, and preserve failed-rollback evidence.

Retention may reclaim eligible successful payloads but never active, failed,
malformed, recovery-incomplete or explicitly retained records. Bound age/count/
bytes, retain compact metadata, provide usage and pretend pruning, and require
explicit destructive action. Never suggest manual deletion as recovery.

Acceptance: hundreds of historical entries cannot bury one active operation;
healthy status is concise. Test old schemas, process death, corrupt records,
pruning boundaries, redirected/no-color output and large history. Presentation
must add no per-entry durability work.
