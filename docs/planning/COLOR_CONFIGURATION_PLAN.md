# Color configuration

Remaining design: one immutable semantic renderer, compatible Portage
`color.map` roles, inspectable provenance and validated overrides. The current
`--color=auto|y|n` and no-color behavior are documented in the installed manuals;
new themes and palette inspection are proposals.

Color supplements text markers. No theme changes words, ordering, JSON or plan
semantics. Only the presentation library emits ANSI and measures styled width.
Honor `NO_COLOR`, `NOCOLOR`, `TERM=dumb`, independent stdout/stderr TTY state and
uncolored machine output. Reject control-sequence injection; degrade terminal
color depth deterministically. Explicit malformed configuration fails visibly;
optional malformed Portage roles warn and retain safe defaults.

Document precedence before adding CLI, environment, user/system and Portage
palette overrides. Add role/provenance inspection and accessible high-contrast,
colorblind and monochrome themes without new dynamic dependencies.

Acceptance: escape-stripped output remains identical across themes, pipes and
TTYs; concurrent progress remains readable; tests cover malformed styles,
color depth, package/USE markers and reference snapshots.
