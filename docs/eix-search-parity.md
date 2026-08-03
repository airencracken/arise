# eix search parity

This document tracks user-visible compatibility between `arise search` and
`eix`. It distinguishes similarly named features from behavior that has been
verified to be equivalent.

## Normal output

The default package report is intended to match `eix` where Arise has the
underlying Portage state:

- package, category, version-status, slot, property, restriction, installed,
  date, USE, USE_EXPAND, title, and overlay-key color roles;
- collected IUSE on the available-versions line;
- host-architecture stable, testing, missing-keyword, and unkeyworded markers;
- installed available-version highlighting and installed USE flag styling.

Arise does not yet model every `eix` distinction. In particular, world,
system, and profile membership do not yet alter package-name colors, and the
overlay numbering policy can differ.

## Implemented analogues

These Arise options cover the common intent, although entries marked partial
do not yet implement the complete `eix` expression semantics.

| eix capability | Arise option | Status |
| --- | --- | --- |
| installed packages | `--installed` | implemented |
| stable/testing packages | `--stable`, `--testing` | implemented |
| masked/care queries | `--masked`, `--care`, `--overflow` | partial |
| duplicate versions | `--duplicates` | implemented |
| world/system membership | `--world`, `--system` | implemented |
| category/name/description | `--category`, `--name`, `--desc` | implemented |
| slot, USE, keywords, license | `--slot`, `--use`, `--keywords`, `--license` | implemented |
| dependency relationships | `--depends-on`, `--required-by` | partial |
| exact, regular-expression, substring search | `--exact`, `--regex`, default search | partial |
| compact/custom/field output | `--compact`, `--format`, `--print` | partial |
| names, count, JSON, dump | `--only-names`, `--count-only`, `--json`, `--dump` | Arise-specific forms |
| version and slot sorting | `--sort` | implemented |

## Missing eix surface

The following groups have no equivalent or only a narrow approximation:

- general nested expression parsing with `--not`, `--and`, `--or`, and
  parentheses;
- multi-installed, slotted, multi-slot, upgrade/downgrade, installed keyword
  state, selected/profile/world-set distinctions, and obsolete-config tests;
- binary-package count and overlay-origin predicates;
- individual RESTRICT and PROPERTIES predicates;
- homepage, SRC_URI, EAPI, installed-EAPI, full-slot, installed-slot,
  installed USE, package-set, and individual dependency-field searches;
- case-sensitive regular expressions, prefix, suffix, wildcard-pattern, and
  fuzzy matching;
- version-line, version-sort, verbose, XML, protobuf, remote-cache, and
  eix-format-language compatibility;
- database introspection such as printing all USE flags, keywords, slots,
  licenses, dependency words, EAPIs, world sets, and profile paths.

## Suggested implementation order

1. Expression parsing and exact field/pattern semantics.
2. Host-effective visibility, mask, world/profile, overlay, and binary state.
3. Remaining field predicates and installed-state predicates.
4. Verbose and per-version output modes.
5. Introspection and alternate serialization formats.

Each promoted item should be tested against local `eix` fixtures before its
status is changed to implemented.
