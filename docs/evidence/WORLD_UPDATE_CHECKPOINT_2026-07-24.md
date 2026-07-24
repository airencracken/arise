# World update checkpoint — 2026-07-24

## Status

This is an intermediate recovery checkpoint, not a clean-world result.

The first deep-update continuation (v15) successfully committed its remaining
seven actions, including `www-client/firefox-140.12.0` and the source-less
`x11-base/xorg-drivers-21.1-r2`. Its verifier then stopped on compatibility and
resolver-parity defects rather than certifying the host.

## Defects exposed and corrected

- Empty package image lists had been serialized as newline-only VDB `CONTENTS`
  files. The affected records were backed up and truncated to zero bytes; new
  merges now emit zero bytes and regression tests cover the case.
- Existing stale built dependency bindings were not included in the fresh
  plan. Four rebuilds were subsequently selected and committed.
- A NUL-padded region in `/var/log/emerge.log` broke `golop`. The log was backed
  up and sanitized; Arise now tolerates historical padding while reading and
  refuses to append to a NUL-corrupt compatibility log.
- `arise sync` updated the repository checkout without publishing a refreshed
  resolver index, producing a false empty world plan. Sync now includes index
  publication before its success message.
- Six installed lifecycle environments leaked stale sandbox/preload controls
  into discovery. Old environments are sanitized at both the caller and worker
  boundary.
- Historical unexpanded `:=` dependencies were mistaken for concrete built
  slot bindings, adding three spurious rebuilds. Resolver rebuild detection now
  requires an explicit installed subslot; new merges persist expanded
  `slot/subslot=` bindings.

## Current continuation

After a fresh repository sync, Arise v22 and Portage selected the same exact
19-package action set: one new slot, 16 upgrades, and two reinstalls. The
download estimate is 1,518,557 KiB. The frozen host-local artifacts are:

- binary: `/tmp/arise-world-refresh-v22`
- binary SHA-256:
  `4c242fbe0a5d1e72fe92c2951215a0688f17909bcb9e4e79a14d707255796e4d`
- preflight: `/tmp/arise-world-refresh-preflight-v22`
- runner: `/tmp/arise-world-refresh-run-v22`

Preflight passed recipe/policy validation for the aligned plan without running
build tools or mutating package state. Execution and the post-run verifier are
still required.

## Gates deliberately left open

- no clean-world or G1 result is claimed;
- obsolete world atoms still require `emaint --check world` repair and motivate
  the planned `arise maintain world --check/--fix` interface;
- the live-mutation switches remain until an unmodified stage3
  maintenance run and its post-boot verification pass;
- host-local continuation scripts and older recovery evidence remain until the
  final transaction is independently verified.
