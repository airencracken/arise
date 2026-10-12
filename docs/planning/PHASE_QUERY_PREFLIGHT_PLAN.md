# Phase queries

Current design combines deterministic static eclass-closure query collection
with the constrained read-only `__phase-query` fallback. It answers only
`has-version`/`best-version` for validated atoms and dependency domains against
the plan-bound ROOT/BROOT VDB. It never probes lifecycle phases or invokes Portage.

Remaining work: classify current repository call sites, add hermetic finite
expansion fixtures and expose static coverage/runtime fallback counts. Preserve
repository precedence, transitive inheritance, negative answers and distinct
query domains. Toolchain, Wine, Java, PHP and CUDA substitutions need broader
repository evidence; historical coverage tables are not current certification.

Acceptance: literal/operator/slot/repository/USE atoms, arrays/loops/brace ranges,
wrappers, nested inheritance, absent candidates, malformed helpers and ROOT/BROOT
separation. Safe dynamic calls must not fail merely because they cannot be
statically enumerated; fallback cannot escape approved roots or run arbitrary
commands. Both layers remain available without Portage Python.
