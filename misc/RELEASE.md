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
