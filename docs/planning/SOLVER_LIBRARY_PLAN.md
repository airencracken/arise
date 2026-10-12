# Solver extraction

Deferred until a real consumer justifies a stable boundary. Keep generic
solver decisions separate from the Gentoo semantic compiler and executor.

The pure-Go core takes immutable normalized candidates/rules/preferences and
returns deterministic selections, structured conflicts and rule provenance.
It accepts cancellation, resource/decision limits and serializable explanations.
It imports no CLI, terminal, filesystem, VDB, Portage, Python or cgo code and
remains usable with `CGO_ENABLED=0`.

Arise retains atom/version, USE/REQUIRED_USE, masks, keywords/licenses,
slot/subslot/blocker/provider, dependency-domain, world and ABI-rebuild semantics.
libsolv and other solvers are references, not runtime dependencies. Do not
weaken Gentoo semantics to fit a generic SAT representation.

Migrate behind an internal boundary first. Promote only after satisfiable,
conflict, preference and explanation fixtures plus deep/newuse Portage matrices
prove unchanged behavior. Require an independent consumer compile test,
versioned API and correctness-equivalent benchmarks before a separate module.
