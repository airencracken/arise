# Releasing Arise

Cross-repository release preparation, verification, publication, asset upload,
and overlay generation are owned by
[`arise-release`](https://github.com/airencracken/arise-release).

This repository retains only its intrinsic build and verification primitives:

```sh
make deps VERSION=x.y.z
make test-vendor-artifact VERSION=x.y.z
```

The final live-system installation is always a separate manual canary.

Before publication, the release tool must also pass `make test-live-self-host`
against its staged source ebuild and verified distfiles. This uses Arise's own
rebuild driver in a disposable root, in addition to Portage's packaging checks.
The ledger's `self_host_verified` field records that gate. See the
[shell-state audit](../docs/audits/SHELL_STATE_CONTRACT_2026-11-05.md).
`make test-live-phase-contract` requires the EAPI 7/8/9 Portage comparisons and
completion QA regression first; missing reference prerequisites fail that lane.
