# Releases

[0.0.50](0.0.50.md) corrects retained-package warnings after external updates
and adds the missing metadata refresh to the Portage bootstrap instructions.

[0.0.49](0.0.49.md) repairs the package shell-state contract, adds Portage
comparison and self-build release gates, and advances the execution ABI to 6.

[0.0.48](0.0.48.md) isolates install QA scripts so Bash completion helpers cannot
leak into saved package environments and break pre-install hooks.

[0.0.47](0.0.47.md) fixes phase-state persistence, the binutils source directory,
and installed lifecycle state, and advances the execution ABI to 5.

[0.0.46](0.0.46.md) fixes recovery capture after protected config updates have
been consumed and preserves the full text of execution errors.

[0.0.45](0.0.45.md) fixes recovery capture of installed filenames containing
spaces, including Intel and Raspberry Pi firmware records.

[0.0.44](0.0.44.md) fixes execution of planned blocker removals and adds
package history queries and dependency-aware time estimates.
It remains experimental and `~amd64`; the full fresh-stage3 acceptance run
remains deferred.

[0.0.43](0.0.43.md) adds profile inheritance and policy inspection, advertised
profile discovery, and configuration comparison, backed by Gentooling.
It remains experimental and `~amd64`; the full fresh-stage3 acceptance run
remains deferred.

[0.0.42](0.0.42.md) covers world-update correctness and latency, resume,
replacement/recovery and remote binary planning.

Earlier version notes remain in this directory. They describe their original
release, not current compatibility. Use the [compatibility matrix](../compatibility/PORTAGE_COMPATIBILITY_MATRIX.md)
for current support and [open work](../../PUNCHLIST.md) for remaining scope.
