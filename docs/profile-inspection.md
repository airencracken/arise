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

## Finding comparison candidates

```sh
arise profile list
arise profile list --all
arise profile list --json
```

`list` reads the configured repositories' `profiles/profiles.desc` catalogs.
It filters entries by the active profile's `ARCH`, and shows the repository,
profile path, advertised status, selected marker and leaf deprecation marker.
`--all` includes every advertised architecture. Status strings are retained as
published by each repository. This file format is defined by the
[Gentoo Package Manager Specification](https://projects.gentoo.org/pms/9/pms.html).

Catalog entries are checked for directory existence and repository containment.
The target's full inheritance graph and policy are loaded when it is compared.
An architecture match is a discovery filter: it does not assess operating
system, libc, ABI, init-system or filesystem-layout transition requirements.
Malformed catalog entries or inaccessible advertised directories fail visibly.
Missing catalog files contribute no entries. The active profile must be readable;
if it has no `ARCH`, use `--all`.

JSON uses the [`arise.profile-catalog.v1` schema](schemas/profile-catalog-v1.schema.json).

## Comparing profiles

```sh
arise profile diff gentoo:default/linux/amd64/23.0/split-usr/desktop/plasma
arise profile diff --explain gentoo:default/linux/amd64/23.0/split-usr/desktop/plasma
arise profile diff --json gentoo:default/linux/amd64/23.0/split-usr/desktop/plasma
arise profile diff gentoo:default/linux/amd64/23.0 gentoo:default/linux/amd64/23.0/desktop
```

With one operand, `diff` compares the active profile with the target. With two,
it compares the explicit before and after profiles, even without an active
selection. Select profiles with `REPOSITORY:PROFILE`, an absolute directory
inside a configured repository, or a unique repository-relative path. The
reserved selector `active` means the current `make.profile`. Ambiguous relative
paths require repository qualification. Unlisted custom profiles inside configured
repositories can also be compared.

The text diff uses red `-` rows and green `+` rows for changed inheritance,
defaults, global USE policy, system packages, package visibility policy and
package USE rules. Scalar values retain quotes, and `(unset)` distinguishes an
absent assignment from an empty string. Incremental variables show token
changes; order-sensitive expressions such as `ACCEPT_LICENSE` retain full values.
Set-valued policy ignores cosmetic ordering and duplicate entries;
package USE rules retain their order and duplicates. Unchanged rules at the
start and end of a changed block are omitted from text output, with counts.
`--explain` includes before and after source contributions for changed settings.
Source-only changes without value or rule changes are omitted.

JSON uses the [`arise.profile-diff.v1` schema](schemas/profile-diff-v1.schema.json).
It includes full before and after rule sequences and provenance for changed
settings, regardless of `--explain`. Both commands honor the global color and
configuration-root options. Exit status is 0 for a successful comparison,
including when differences exist, 1 for inspection/output failure, and 2 for
invalid arguments. Failures do not emit a partial report.

Comparison loads both profiles with the same evaluator as `show`. It isolates
profile contributions, leaves the active selection and configuration untouched,
and does not plan rebuilds or perform a profile migration. The graph adapter
uses a private temporary symlink which is removed after inspection. Files
outside supported report policy remain listed for each side.

Targeted `why` queries are not implemented yet.
