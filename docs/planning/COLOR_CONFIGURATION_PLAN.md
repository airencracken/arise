# Color and presentation configuration plan

## Goal

Arise color must communicate documented package-management semantics. It must
not be an undocumented collection of calls to `Green`, `Red` and `Bold`.
Users must be able to inspect, explain and override the palette without losing
information, breaking redirected output, or adding a runtime dependency.

Color is supplementary. Every state remains encoded in text markers, labels,
field positions or structured JSON so monochrome and assistive use is complete.

## Compatibility baseline

Portage's `/etc/portage/color.map` and semantic style names are the initial
compatibility reference. Relevant established roles include `GOOD`, `BAD`,
`WARN`, `QAWARN`, `INFORM`, `PKG_MERGE`, `PKG_BINARY_MERGE`, `PKG_UNINSTALL`,
`PKG_NOMERGE`, blockers and prompt choices.

Arise should read compatible `color.map` entries where a Portage role maps
cleanly to an Arise role. Arise-specific roles may extend this vocabulary but
must not silently reinterpret an established Portage name.

## Semantic role model

Rendering code requests a role rather than a physical color. The initial role
families should include:

- severity: `success`, `error`, `warning`, `qa-warning`, `information`;
- package source: `package.source`, `package.binary`, `package.installed`;
- operation: `operation.new`, `operation.reinstall`, `operation.upgrade`,
  `operation.downgrade`, `operation.remove`, `operation.blocked`;
- identity: `package.atom`, `slot`, `repository`, `installed-version`;
- USE state: `use.enabled`, `use.disabled`, `use.changed`, `use.added`,
  `use.removed`, `use.forced`, `use.masked`;
- progress: `progress.count`, `progress.load`, `progress.active`;
- prompt: `prompt.default`, `prompt.alternative`, `prompt.danger`;
- search/query: `match`, `masked`, `unstable`, `restriction`, `metadata-label`.

Some displayed values combine roles. For example, an enabled USE flag that
changed state carries both `use.enabled` and `use.changed`; the theme resolves
that combination deterministically. Parentheses, `*`, `%`, signs and action
columns remain the authoritative non-color meaning.

## Single presentation library boundary

All styling belongs to one library. Commands, resolver output, progress code
and package renderers must not call physical helpers such as `Green`, embed ANSI
escapes, inspect terminal capabilities, or implement their own no-color logic.

The library owns:

- semantic role definitions and combinations;
- compiled defaults, themes and configuration precedence;
- stdout/stderr terminal and color-depth detection;
- ANSI generation, reset discipline and escape stripping;
- visible-width measurement, truncation and wrapping of styled text;
- `NO_COLOR`, `NOCOLOR`, `TERM=dumb`, JSON and redirected-output policy;
- safe validation that prevents arbitrary control-sequence injection;
- sample/legend and provenance data for `arise config colors`.

Call sites receive an immutable renderer (or narrowly scoped style function)
and ask it to render a semantic role. For example:

```go
renderer.Style(color.RoleUseEnabledChanged, flag)
renderer.Style(color.RoleOperationUpgrade, "U")
```

They do not choose green/cyan/bold themselves. Plain text remains available
from the same renderer, so colored and uncolored paths cannot drift.

The current `internal/color` package is the natural migration point but not yet
the finished boundary: its mutable global `UseColor` and physical-color helper
API must be replaced rather than propagated.

## Explainability

Provide an inspection command such as:

```text
arise config colors
arise config colors --explain use.enabled.changed
arise config colors --samples
```

The output should show:

- semantic role and plain-language meaning;
- resolved style (foreground, background and attributes);
- source and precedence layer for the value;
- corresponding Portage role, if any;
- colored sample and exact monochrome representation;
- whether color is currently active and why (`auto`, CLI, environment, TTY,
  `TERM=dumb`, JSON, or redirected output).

The manual and generated configuration reference must include the same legend.

## Configuration and precedence

Use a small dependency-free parser suitable for the static recovery binary.
Do not introduce a dynamic configuration library merely for themes.

Proposed precedence, highest first:

1. explicit CLI (`--color=always|auto|never`, theme or role override);
2. one-shot Arise environment overrides;
3. user Arise configuration when a user configuration mode is implemented;
4. system Arise color configuration;
5. compatible `/etc/portage/color.map` roles;
6. compiled Arise defaults matching emerge where semantics overlap.

`NO_COLOR` always disables color unless the user explicitly requests
`--color=always`. Portage's `NOCOLOR` must also be honored. JSON and other
machine formats never contain terminal escapes. In `auto` mode, stdout and
stderr are evaluated independently, and `TERM=dumb` disables styling.

An initial system location may be `/etc/arise/color.conf`, but it should be
chosen as part of the broader Arise configuration-layout design rather than
creating isolated files ad hoc.

## Style grammar

The accepted vocabulary should support:

- named ANSI colors compatible with Portage;
- 256-color indexes and true-color RGB values where terminal capability permits;
- attributes such as bold, underline and reverse;
- `none`/inheritance and role aliases;
- foreground and background independently;
- comments and precise line-numbered errors.

Invalid styles fail visibly for explicit Arise configuration. A malformed
optional Portage compatibility entry should produce a warning and retain the
safe compiled default, matching the surrounding configuration error policy.

Terminal capability degradation must be deterministic: true color to 256,
then 16 colors, then monochrome. Themes cannot emit unsupported control
sequences blindly.

## Accessibility and themes

Ship at least:

- `emerge`: Portage-familiar defaults;
- `high-contrast`: avoids low-luminance distinctions;
- `colorblind`: avoids red/green as the sole visual distinction;
- `mono`: attributes only, useful for limited consoles.

Theme selection never changes words, markers, ordering or JSON. Snapshot tests
strip escape sequences and prove informational equivalence across every theme.

## Implementation sequence

1. Inventory every existing hard-coded color call and assign a semantic role.
2. Make `internal/color` the sole presentation library, add immutable
   style/theme types and a renderer-local palette, and remove mutable global
   `color.UseColor` as configuration migrates.
3. Reproduce the current/emerge palette through semantic roles with no output
   grammar changes.
4. Parse and map `/etc/portage/color.map`.
5. Define the broader Arise configuration layout and add role overrides.
6. Add `arise config colors`, explanations, samples and documentation.
7. Add accessibility themes and terminal-depth degradation.
8. Audit all TTY owners, including progress, prompts, errors, search and plans.
9. Add a repository check rejecting raw ANSI escapes and physical color-helper
   calls outside the presentation library and its tests.

## Test matrix

- TTY versus pipe/file independently for stdout and stderr;
- `--color=auto|always|never`, `NO_COLOR`, `NOCOLOR`, `TERM=dumb`, JSON;
- default, Portage-compatible, custom, high-contrast, colorblind and mono themes;
- 16-color, 256-color and true-color capability degradation;
- every package action marker and USE transition;
- malformed configuration, unknown roles and unsafe escape injection;
- concurrent output through the durable terminal status-line owner;
- escape-stripped equality with monochrome output;
- representative emerge differential snapshots.

## Acceptance gates

- Every emitted style has a named semantic role and documented meaning.
- Only the presentation library emits or interprets terminal styling.
- `arise config colors` explains the effective palette and provenance.
- Default plan colors and markers match emerge for the parity corpus.
- No-color output loses no information.
- Redirected and JSON output contain no escape sequences.
- Configuration parsing remains available in the static standalone binary.
- Color customization cannot inject arbitrary terminal control sequences.
