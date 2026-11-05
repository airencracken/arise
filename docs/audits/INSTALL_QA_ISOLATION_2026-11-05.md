# Install QA shell isolation

The 0.0.47 phase-state audit missed a shell boundary: Arise sourced install QA
checks directly into the package worker. Portage's `misc-functions.sh` runs
each check in a subshell. QA checks may inspect and modify the image, but their
functions and other shell state must not become package state.

The reported caddy-analyzer 0.8.1 failure identifies the exact consequence.
Portage's `60bash-completion` check sources the system Bash completion library
when the image contains completions. That library enables `extglob` and defines
`_comp_abspath`, whose `case` includes `/..?(/*))`. Arise serialized the leaked
function into `phase.environment`. The next worker parsed the saved function
without `extglob` and failed before `pkg_preinst` could run.

The [Bash manual](https://www.gnu.org/software/bash/manual/html_node/Pattern-Matching.html)
explains that extended patterns require `extglob` while parsing a function.
Enabling that option globally would conceal the unwanted QA state in this
case; the repair restores the intended boundary instead.

## Repair and verification

Each QA check now runs in a separate subshell, with the QA driver's defaults
also confined to a subshell. Image edits and diagnostics survive. Variables,
readonly values, functions, working directories, options, and traps remain
local to the check. Failed QA scripts report an error and permit subsequent
checks, following Portage's advisory QA execution policy.

The live regression uses the installed Portage `60bash-completion` check and
Bash completion 2.18.0. Before the fix it fails in `pkg_preinst` with the same
`/..?(/*))` syntax error. After the fix the next worker loads the snapshot and
the hook completes without leaked completion helpers or options.

Portable tests check isolation between successive QA scripts and from QA into
the next package phase. They verify that image edits survive and that ordinary
failure, `exit`, and `die` are reported without suppressing subsequent checks.
The existing QA event/finalization test remains enabled. EAPI 7/8/9 Portage
comparisons and the actual binutils unpack/prepare test are rerun as well.

Before/after logs are retained under `.validation/qa-shell-isolation/` in the
primary checkout. This reproduces the reported install/QA/pre-install boundary;
it does not compile the complete caddy-analyzer application or execute the host
upgrade transaction.
