# GentooPM reference audit

> Development architecture note; reviewed from `gentoo/gentoopm` master on
> 2026-07-17. GentooPM is prior art, not a runtime dependency of Arise.

GentooPM wraps Portage and pkgcore behind a package-manager-neutral Python API.
Its stated goals—portable tooling and a readable interface over package-manager
internals—closely match Arise's long-horizon Go library goal.

## Useful boundaries

The source separates these concepts cleanly enough to inform Go package design:

- package keys, versions and atoms;
- conditional, all-of, any-of, exactly-one and at-most-one dependency nodes;
- package sets plus filtered and sorted views;
- installed versus installable packages;
- repository collections and repository stacks;
- package contents;
- configuration, USE and USE_EXPAND descriptions;
- package-manager-specific adapters.

Arise should preserve those conceptual boundaries in narrow immutable Go APIs.
They align with existing internal atom, dependency-expression, repository/VDB,
profile/configuration and snapshot work and would let query, audit, dispatch and
third-party Gentoo tools reuse the model without importing the Arise CLI.

## What not to copy

GentooPM relies heavily on Python inheritance and adapters around mutable PM
internals. That makes a common API possible, but also couples semantics and
lifetime to the underlying implementation. Arise libraries should instead use:

- immutable value records and explicit snapshots;
- small capability-oriented interfaces at integration boundaries;
- typed errors and structured conflict/plan records;
- deterministic iteration and serialization contracts;
- conformance fixtures shared by the library and CLI;
- no dependency on live global Portage process state.

The first extraction should follow proven internal boundaries rather than begin
with a broad compatibility facade. Atoms/dependency expressions and immutable
repository/VDB/profile snapshots are the strongest initial candidates.

## Resolver relevance

GentooPM exposes the dependency expression shape but delegates solving to the
underlying package manager. Portage's own `dep_zapdeps` remains the authoritative
reference for choice preference and graph-aware `want_update` behavior. Arise's
resolver must keep those semantics in its own deterministic transaction model;
GentooPM is primarily useful for public object-model and query API design.
