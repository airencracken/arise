# Punch-list audit — 2026-07-17

> Historical development audit; retain for context until superseded or pruned.

## Findings

1. P1 is complete at its stated model boundary. Portable corpus packaging is
   hardening work, not evidence that repository/VDB modeling is unfinished.
2. P2 implementation is substantially complete. Its remaining gates are
   package.env consumption, privilege-boundary behavior and portable snapshot
   promotion.
3. P3 is the principal correctness blocker. No build or merge plan may become
   executable until whole-state verification is mandatory and feeds failures
   back into search.
4. P4 has two implementations: the new versioned Bash protocol and an older
   synthetic phase runner. The latter contains silent success behavior and must
   be retired rather than extended in parallel.
5. P5 now has one authoritative Manifest-backed acquisition path, but the old
   `Fetch`/`FetchFile` API remains as dead production code with stale behavior.
   It should be removed rather than maintained as a second downloader.
6. Several P5 test boxes were stale: digest corruption, concurrency and cached
   offline rebuild coverage already exist and are now marked complete.
7. Live-machine numbers are evidence tied to a repository snapshot, not
   timeless current state. Claims are now dated and portable fixture promotion
   remains explicit.
8. The phase worker intentionally uses explicit status capture rather than
   `set -e`, `set -u` or `pipefail`. A future public Bash runtime should turn
   that safety property into reusable, tested APIs for eclasses and external
   Gentoo tools.

## Correct critical path

1. Remove or fail closed every obsolete silent-success path (P0B).
2. Freeze portable P2/P3 parity and broken-world fixtures before the laptop is
   repaired or repositories change materially.
3. Make the P3 whole-state verifier mandatory and search-integrated.
4. Complete the P4 protocol preflight, environment and phase/eclass discovery.
5. Complete P5 delivery through resumable verified artifacts and policy.
6. Build the P4 helper/lifecycle ABI against those verified artifacts.
7. Begin P6 journaled isolated-ROOT mutation only after P3/P4 acceptance gates.

## Immediate cleanup pass

- Delete the unused legacy downloader and its stale mirror stub.
- Change legacy FEATURES and archive handling from log/skip success to typed
  rejection until the versioned protocol owns those behaviors.
- Keep install/update/removal mutation gates in place.
