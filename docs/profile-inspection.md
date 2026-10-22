# Inspecting profile inheritance

```sh
arise profile show
arise profile show --explain
arise profile show --json
arise --portage-config-root /path/to/etc/portage profile show --explain
```

`show` reads the selected `make.profile` and follows its parents, including
cross-repository parents resolved through the configured repositories. It prints
each inherited layer once, in evaluation order. With `--explain`, it also prints
each layer's parent references relative to that layer.

The report contains:

- Merged `make.defaults` assignments, using Arise's configuration evaluator.
- The profile's `@system` entries from `packages` and `package.provided` entries.
- Global USE masks and forces, including stable-only policy.
- Profile `package.mask` and `package.unmask` entries.
- Ordered package USE defaults, masks, and forces, including stable-only rules.

`--explain` adds the contributions in evaluation order with their physical file
and line number. Earlier assignments, removals and overrides remain visible.
Backslash continuations point to the first physical line. For example:

```text
  CFLAGS="-O2 -pipe"
    /repo/profiles/base/make.defaults:10: "-O1"
    /repo/profiles/desktop/make.defaults:4: "-O2 -pipe"
```

Negative USE tokens in merged defaults retain explicit disable requests.
Package USE sections contain ordered rules: the final flag state depends on a
package's version, slot, repository, stability and IUSE. Use `arise inspect ATOM`
for package-specific effective USE and visibility on the configured system.

This report isolates the profile's contributions. It excludes `make.globals`,
local `make.conf` and package policy, the command environment, and package IUSE.
Repository-wide mask files are excluded unless their directory is itself a
layer of the selected profile. Profile `package.unmask` entries are shown
separately from `package.mask`; they are applied when visibility is evaluated.

Arise supports declarative assignments and does not execute shell hooks or
scripts. Files outside the supported report, including `profile.bashrc`, are
listed under `unsupported_files`; their effects are not included. The report
does not evaluate `eapi` or `deprecated` metadata. Unsupported directory-based
profile policy rejected by the shared graph loader produces an error.

JSON output always includes provenance, with or without `--explain`. It uses
the [`arise.profile.v1` schema](schemas/profile-v1.schema.json), with defaults
sorted by variable name, layers and contributions in evaluation order, and
empty lists represented as arrays. `scope` is always `profile`. Policy `kind`
distinguishes effective entries from `ordered_rules` requiring package context.

The command only reads configuration and does not need an Arise index or root
privileges. Exit status is 0 on success, 1 for inspection or output failure,
and 2 for invalid arguments. Missing profiles, parent cycles, invalid parent
paths and unreadable policy fail the command without emitting a partial report.

Profile comparison and targeted `why` queries are not implemented yet.
