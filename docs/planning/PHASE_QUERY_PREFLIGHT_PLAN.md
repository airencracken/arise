# Phase query preflight parity plan

## Goal

Every `has_version` and `best_version` call reachable from an ebuild's complete
eclass closure must be answered against the state-bound ROOT or BROOT VDB
selected for the approved plan. Static preflight should answer every query it
can prove before build work starts. Dynamic queries that cannot be enumerated
safely may use the constrained read-only runtime helper, but must never invoke
Portage, execute lifecycle discovery probes, or observe an unapproved package
database.

The implementation remains static and standalone. Executing lifecycle phases
merely to discover queries is not safe: Portage permits `pkg_setup` and
installed lifecycle hooks to affect the host.

## Implementation status — 2026-07-24

The original plan required complete static enumeration and treated any
runtime-discovered query as a failure. Live Gentoo eclasses demonstrated that
arbitrary version, slot, USE-dependency, and query-domain substitutions make
that requirement incomplete without implementing a general Bash interpreter.

Arise now uses two layers:

1. deterministic static collection and finite expansion for known query sites;
2. a narrowly dispatched `__phase-query` helper for unresolved calls.

The helper accepts only `has-version` or `best-version`, a validated dependency
domain, one atom, and the phase USE state. It reads the ROOT/BROOT VDB paths
provided by the phase policy and returns only the query result. It is not an
arbitrary subprocess escape and cannot mutate package state. Domain-specific
answers remain distinct.

## Current coverage

| Query family | Status | Representative source |
| --- | --- | --- |
| Literal quoted/unquoted `has_version` atoms | covered | meson, cmake, ninja-utils |
| Ebuild-variable expansion in quoted atoms | covered | Go bootstrap ebuilds |
| Complete transitive eclass source closure | covered | gstreamer-meson → python-any-r1 |
| Python implementation and brace ranges | covered | python-any-r1, distutils-r1 |
| Python `PYTHON_REQ_USE` variants | covered | Python interpreter probes |
| Arbitrary `${PYTHON_USEDEP}` atoms | covered | GLib setuptools probe |
| Arbitrary `${PYTHON_SINGLE_USEDEP}` atoms | covered | python_check_deps consumers |
| Vala installed-slot loop | covered | vala.eclass |
| Autoconf/Automake preference arrays | covered | autotools.eclass |
| LLVM finite slot loops | covered | llvm.eclass |
| Rust finite package/slot loops | covered | rust.eclass |
| Query-domain identity (`-b`, `-d`, `-r`) | covered by domain-aware static/runtime contract | LLVM, Rust, toolchain |
| Toolchain bootstrap slot/version loops | constrained runtime fallback; audit pending | toolchain.eclass |
| Wine package/ABI substitutions | constrained runtime fallback; audit pending | wine.eclass |
| Java VM/dependency variables | constrained runtime fallback; audit pending | java-vm-2, java-utils-2 |
| PHP version-derived atoms | constrained runtime fallback; audit pending | php-ext-source-r3.eclass |
| CUDA compiler/toolkit `best_version` chains | constrained runtime fallback; audit pending | cuda.eclass |
| Arbitrary constrained `best_version` atoms | covered by constrained runtime contract | CUDA, PHP, toolchain |

## Architecture

1. Build a deterministic recursive eclass closure using repository priority.
2. Parse both the ebuild and every source in that closure once.
3. Collect literal queries for both APIs.
4. Expand finite declarations and known Portage eclass variables into exact
   atoms, including negative answers for absent candidates.
5. Evaluate every statically enumerated atom against the selected ROOT/BROOT VDB
   snapshots.
6. Preserve query domain with every static answer and runtime request.
7. Route unresolved calls only through the constrained helper bound to the same
   VDB roots; reject malformed domains, atoms, helper output, or invocation.
8. Record static coverage and runtime fallback use so repository audits can
   migrate common dynamic families into deterministic preflight over time.

## Test matrix

Hermetic fixtures must cover literal atoms, operators, slots, repositories, USE
dependencies, ROOT/BROOT switches, wrapper functions, arrays, loops, brace
ranges, nested transitive inheritance, absent candidates, constrained
`best_version`, runtime-helper domain separation, invalid helper output, and
helper unavailability. A repository audit should classify all current Gentoo
eclass call sites, while correctness tests remain independent of
`/var/db/repos`.

## Completion criteria

- Every current Gentoo eclass query site is classified in the matrix.
- Every finite query generator has a hermetic regression fixture.
- Static-preflight coverage and runtime-fallback calls are observable.
- No representative deep/newuse world run fails because a safe query was not
  statically preflighted.
- Runtime queries cannot escape the approved ROOT/BROOT VDB domains or invoke
  arbitrary commands.
- The default static build needs no Portage Python runtime for either layer.
