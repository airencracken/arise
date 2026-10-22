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
each layer's parent references relative to that layer. The selected layer is
marked `[selected]`. Paths below the displayed `Root` use relative names;
cross-repository paths outside that root remain absolute.

Text output aligns variable names and values, package atoms and flags, and
sources and contributions in separate columns. Long values wrap under the
value column at the terminal width, or 100 columns for redirected output.
Individual tokens and long source paths are preserved without truncation.

Section headings and variable names use Arise's cyan palette, source locations
use blue, positive USE tokens use green, and negative tokens use red. Global
USE masks and package masks use red. Colors supplement the policy names and
token signs. Automatic colors respect terminal detection, `NO_COLOR`, `NOCOLOR`
and `TERM=dumb`. Use the global color option to override automatic detection:

```sh
arise --color=y profile show --explain
arise --color=n profile show --explain
```

Redirected output is uncolored in automatic mode. JSON output is always
uncolored, including with `--color=y`.

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
  VARIABLE  VALUE
  CFLAGS    "-O2 -pipe"
    SOURCE                    CONTRIBUTION
    base/make.defaults:10     "-O1"
    desktop/make.defaults:4   "-O2 -pipe"
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
