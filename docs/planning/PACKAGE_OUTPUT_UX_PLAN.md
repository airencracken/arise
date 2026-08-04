# Package Plan and Progress Output UX Plan

Implementation note (updated 2026-08-04): dense plan rows now use Portage-style action
fields with independently colored package type and N/R/U/D markers, selected
and replaced identities, USE transitions/USE_EXPAND groups, and per-package
download sizes. Runtime output distinguishes image installation, validation,
content installation/sync, transaction commit, and lifecycle finalization; it
also reports completed jobs and load averages. Post-transaction reporting now
covers lifecycle notices, Info-index results, preserved libraries, protected
configuration updates, and unread news.

Successful per-distfile checks, downloads, and Manifest verification are now
suppressed in default execution output and retained by `--verbose`. Durable
package lifecycle stages use semantic terminal color, while automatic
redirected output and JSON remain uncolored. Runtime vocabulary now uses
`staging area` and `package contents` rather than exposing the internal package
image implementation.

## Goal

Arise's default package line must implement emerge's established package-line
grammar and marker semantics. Gentoo users should not need to learn a second
dialect to understand a plan. The installed Portage implementation in
`_emerge/resolver/output.py` and `output_helpers.py` is the executable reference;
its behavior, not an independently invented approximation, defines parity.

Arise-specific information belongs in optional, clearly labeled continuation
fields, verbose/explain modes, or JSON. Deviations from emerge's default syntax
require a documented usability or machine-interface reason and compatibility
tests demonstrating that no information was lost.

## Default package record

The first line should contain:

- artifact type: source ebuild or binary package;
- operation: new install, update, downgrade, reinstall, slot move, removal;
- selected CPV, slot/subslot, repository and dependency domain;
- installed CPV, slot/subslot and repository for transitions;
- concise rebuild-cause markers;
- per-package download bytes still required;
- blockers, masking or keyword state when relevant.

Example, matching emerge's shape:

```text
[ebuild  U  ] media-video/vlc-4.0.0_pre20260515:0/12-9::gentoo
             [4.0.0_pre20260418:0/12-9::gentoo]  download: 0 KiB
```

Implement Portage's attribute columns exactly: interactive `I`, new/forced
reinstall `N`/`r`, new slot/replacement `S`/`R`, fetch restriction `F`/`f`,
upgrade `U`, downgrade `D`, and mask status. Use the same column positions and
spacing. Color supplements these text markers; redirected output, `NO_COLOR`,
early boot and recovery consoles retain every distinction.

## USE and configuration state

The plan must group flags in Portage-compatible domains:

```text
USE="X alsa dbus -aom -debug"
CPU_FLAGS_X86="sse"
LUA_SINGLE_TARGET="lua5-1"
```

Reproduce `_create_use_string` semantics, including:

- enabled and disabled flags;
- `*` for enabled-state changes;
- `%` for IUSE additions/removals and `%*` combinations;
- parentheses around forced or masked flags;
- parentheses around removed-IUSE flags;
- unavailable on the current architecture or profile;
- user-explicit versus default-derived where explain mode is requested;
- USE_EXPAND group and order;
- values that trigger the rebuild versus unchanged context.

Follow emerge's verbosity and `--alphabetical` behavior for complete versus
changed flag display and enabled/disabled/removed ordering. A future compact
mode may exist, but it must be explicit and must not replace parity defaults.

## Required action-model data

Extend the immutable planned action with:

- selected artifact kind and binary provenance;
- installed CPV, slot, subslot, repository and domain;
- installed USE and IUSE snapshots;
- effective selected USE grouped by USE_EXPAND variable;
- flag state/provenance markers;
- normalized rebuild causes, including triggering provider/edge;
- per-action required download bytes and known local reuse;
- keyword, mask and license state where it affects selection.

These fields belong in the versioned JSON plan and canonical plan digest. The
renderer must not reread mutable VDB or configuration state after plan
authorization.

## Progress vocabulary

Plan records follow emerge. Runtime progress is an Arise extension and uses
separate, unambiguous stages:

```text
Building package
Installing into staging area
Validating package contents
Preparing package transaction
Installing package contents: 3,817/7,000 entries
Syncing package contents
Committing package transaction
Running post-install actions
Completed package
```

Avoid implementation-only nouns such as `image`, overloaded nouns such as
`paths`, and labels that imply one durability operation per progress entry.
Verbose timing should break down build, staging, validation, preimage capture,
content installation, group sync, commit and post-install lifecycle.

## Presentation modes

- Default: emerge-density decision records and concise runtime stages.
- `--compact`: one package line with transition and only meaningful deltas.
- `--verbose`: complete flag state, causes, provenance and stage timing.
- `--json`: complete versioned data without terminal formatting.
- `--quiet`: failures and final summary only.

Terminal wrapping must indent continuation lines deterministically. Never
truncate CPV, slot, repository, flag names, operation IDs or commands needed
for recovery. Extremely long USE state may wrap but remains one logical record.

## Acceptance criteria

- The selected and installed identities can be compared without consulting
  eix, VDB or another command.
- Every emerge USE marker needed to understand a rebuild has an Arise text and
  JSON equivalent.
- Per-package sizes sum exactly to the printed total download requirement.
- Source and binary actions are distinguishable without color.
- A package selected solely for subslot, USE, dependency metadata or
  preserved-library reasons says so on the package record.
- Redirected and colored terminal output normalize to identical information.
- Default successful fetch output is bounded by package rather than distfile
  count; verbose mode retains the complete artifact lifecycle.
- Snapshot tests cover new install, update, downgrade, reinstall, slot move,
  binary reuse, masked/forced flags, USE_EXPAND, and long wrapped records.
