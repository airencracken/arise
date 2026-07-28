# C2 parsed dependency edge handoff

Date: 2026-07-27

## Result

The graph-to-resolver conversion now passes already parsed dependency atoms
directly to the resolver. It no longer serializes every atom and immediately
parses it again. The resolver copies the top-level atom value for each edge so
its existing mutable-edge contract remains intact while immutable parsed
version data is shared.

All live runs produced the same verified result:

- plan SHA-256:
  `143d194ae78ba3a9b1a6f7517051be06087808345367ee7f37d43a1a081afef4`
- state SHA-256:
  `4c7a0233cce4d97ceec98ffc67c2fb65978b6cc4b87402a35691e8f67806c8e7`
- verification: whole-state verified

## Interleaved live result

The workload was a warm, read-only, deep/newuse, complete-graph `@world`
update with five alternating baseline/candidate runs. Times are seconds and
RSS is KiB.

| Revision | Median wall | Median CPU | Median peak RSS |
| --- | ---: | ---: | ---: |
| previous accepted C2 build | 2.76 | 4.97 | 740248 |
| parsed-edge handoff | 2.48 | 4.63 | 568604 |
| change | -10.1% | -6.8% | -23.2% |

Command shape:

```sh
/usr/bin/time -f '%e %U %S %M' arise \
  --pretend --update --deep --newuse --complete-graph \
  --with-bdeps=y --keep-going --backtrack=20 \
  --resolver-timeout=5m --json install @world
```

The candidate allocation profile reported 733.20 MiB total allocation. The
remaining snapshot decoder retention is primarily the metadata string payload,
so changing its durable schema was rejected without evidence of avoidable
duplication.

## Rejected reverse-edge sizing pass

An exact-capacity reverse-edge implementation counted incoming edges before
building the reverse lists. Three interleaved pairs regressed median wall time
from 2.49 to 2.54 seconds and increased median peak RSS from 562856 to 570840
KiB. The counting pass and its tests were removed.

## Regression fixture

`BenchmarkToResolveGraphParsedEdges` freezes a 5,000-package chain fixture and
reports time and allocation counts for the optimized conversion boundary. It
is intended for same-host `benchstat` comparisons; live correctness-gated
results remain the acceptance authority because fixed absolute CI timing
thresholds would be host-dependent.
