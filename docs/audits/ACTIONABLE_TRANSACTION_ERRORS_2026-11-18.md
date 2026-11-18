# Actionable package transaction errors

The sweep followed an update from resolution through preflight, source fetch,
build, merge, and interruption. The starting case was Firefox 153.7.0 failing
its build-space check while normal Arise output showed only the exit code.

| Failure | What the default diagnostic now gives the operator |
| --- | --- |
| Package preflight | The package, phase, and ebuild instructions. A recognized Gentoo build-space check includes the stable work directory, available MiB, shortfall when measurable, and the option to choose a different build filesystem. Read-only preflight uses the same explanations. |
| Restricted or unverifiable distfiles | The destination for a manual download, each failed source and its cause, and the Manifest and received values for a checksum mismatch. Fetch-only reports each failed package in plan order and honors the same evaluated fetch restrictions as a build. |
| Ebuild phase failure | Package-authored error messages take priority over the bounded compiler excerpt. The build log remains available. Live notices carry the ebuild's error, warning or information class. |
| File ownership collision | Conflicting paths, the known owner when ownership records identified one, and the read-only ownership query. A locally existing file can have no package owner. |
| Journal, logging and interruption | A failure to write the merge log keeps its path and filesystem cause. Concurrent cancellation preserves independent errors. Post-commit and rollback failures retain their outcome. |
| Resuming | Interruption reports whether the selected checkpoint exists. A missing checkpoint suggests a fresh preview or the alternate checkpoint from the earlier transaction. An unreadable or malformed checkpoint directs the user to read-only inspection. |

## Comparison with Portage

A disposable `emerge --buildpkgonly --nodeps` run used an EAPI 8 test package
that inherited Gentoo's `check-reqs` and requested 9,700 MiB. Its preflight
identified the package, failed requirement, temporary path and ebuild advice.
With the same ebuild, Arise's install and `--preflight-only` commands both
reported 5,034 MiB available and a 4,666 MiB shortfall on the disposable build
filesystem. Arise's normal error renderer was also exercised with the real Firefox
153.7.0 ebuild on a filesystem below that threshold. Arise printed the
requirement and advice, measured the remaining space before removing the
temporary directory, and identified the work filesystem the operator can change.
A measurement describes space at check time. If measurement fails, Arise still
shows the requirement. Memory and target-filesystem checks keep the ebuild's
own advice.

The source comparison also covered Portage's fetch-restriction and collision
handling. Fetch tests used a local HTTP server, a manual-only artifact, and a
same-size corrupt response. Merge tests used conflicting VDB ownership in a
disposable root. Log tests combined a failed write with cancellation and with
an independent build error. Resume tests covered missing and malformed state.

## Scope

Resolver mask, keyword, USE and license explanations, incompatible binary ABI,
rollback recovery and failed post-install handling already have distinct
diagnostic paths. This sweep did not change resolver choices or compare every
command against emerge. Binary-host transport, repository sync and maintenance
subcommands remain candidates for separate failure-injection tests.
