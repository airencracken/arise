# Arise performance baseline — 2026-07-16

This is the first correctness-gated end-to-end baseline produced by
`arise-perf`. It is a development baseline, not a release performance claim.

## Snapshot

- Gentoo repository: `6cab62289e9a2cb34ec66247cb739fe570b75e40`
- Profile: `default/linux/amd64/23.0/split-usr/desktop`
- VDB identity: SHA-256 of sorted installed VDB directory names,
  `895b1497555b0e2fc9757b2a0c7a9bb165b2a790566ec3d7cf2bc75a60322da7`
- Host architecture: amd64
- Arise: development build using Go 1.26.3
- eix: 0.36.9
- Portage: 3.0.77

Raw reports were written under `/tmp` during the run. Workload definitions are
versioned in `misc/perf-*.json`.

## Results

| Workload | Equivalence | Arise median | Reference median | Arise speedup |
|---|---:|---:|---:|---:|
| All installed CPVs | yes | 8.14 ms | eix-installed 36.86 ms | 4.53x |
| Firefox substring package names | yes, **performance failure** | 1.281 s | eix 32.67 ms | 0.026x |
| Firefox substring package names | yes, **performance failure** | 1.306 s | emerge 868.03 ms | 0.665x |
| Signal pretend plan | **no, correctness failure** | 3.237 s | emerge 3.301 s | timing invalid |

The installed-listing result compares identical sorted CPV output hashes.

The search comparison uses package-name normalization because Arise
`--search-only-names` prints package names while eix prints category/package.
The selected result sets are equivalent for this unambiguous query.

## First optimization checkpoint

The first search pass removed repeated VDB scans and avoids decoding every
repository metadata record for name-only substring searches. On the same
frozen snapshot it produces the same result set and now measures:

| Workload | Equivalence | Arise median | Reference median | Arise speedup |
|---|---:|---:|---:|---:|
| Firefox substring package names | yes, **performance failure** | 140.57 ms | eix 32.31 ms | 0.230x |
| Firefox substring package names | yes, **pass** | 141.68 ms | emerge 859.18 ms | 6.06x |

This closes the emerge performance gate without weakening correctness. The
eix gap remains a blocker and motivates a compact secondary name-search index
instead of further full Badger key scans.

## Immutable name-index checkpoint

Arise now generates a versioned, checksummed, atomically replaced package-name
sidecar alongside the canonical Badger metadata database. The same frozen
snapshot and normalized output set measure:

| Workload | Equivalence | Arise median | Reference median | Arise speedup | Arise cache | Reference cache |
|---|---:|---:|---:|---:|---:|---:|
| Firefox substring package names | yes, **pass** | 10.68 ms | eix 33.70 ms | 3.16x | 31.00 MiB | 25.80 MiB |
| Firefox substring package names | yes, **pass** | 11.62 ms | emerge 862.26 ms | 74.22x | 31.00 MiB | n/a |

The name sidecar itself is 451 KiB. Arise's total is larger because the 31 MiB
also includes its rich resolver metadata store; the eix figure is its 25.80 MiB
search cache. Future index benchmarks must report full/incremental build time,
total cache bytes, and cold/warm queries together.

## Index-build checkpoint

Full and no-change builds now use post-build complete package-set validation:

| Workload | Equivalent | Arise median | eix-update median | Speedup | Arise cache | eix cache |
|---|---:|---:|---:|---:|---:|---:|
| Full index | yes | 1.29 s | 4.22 s | 3.27x | 30.82 MiB | 25.77 MiB |
| No-change index | yes | 713 ms | 4.22 s | 5.92x | 30.82 MiB | 25.77 MiB |

The isolated `emerge --metadata` workload is versioned but awaits a privileged
run because Portage correctly enforces root/portage-group cache permissions.

## Initial findings before optimization

1. Direct VDB scanning in Go already materially outperforms eix-installed.
2. Arise's current search CLI path is approximately 38 times slower than eix
   and 1.5 times slower than emerge despite favorable in-process
   microbenchmarks. This is a release-blocking bug.
3. CLI startup, Badger open/read behavior, schema layout, and full result
   collection must be profiled before optimizing search matching code.
4. Query performance budgets must measure complete processes, not only package
   functions inside `go test -bench`.
5. Resolver comparisons remain correctness-blocked by the known Signal plan
   mismatch; their timing cannot be counted as a valid speedup yet.
6. The harness now rejects equivalent results below the workload's speed floor;
   the default floor is 1.0x and milestone workloads should demand more.

## Next benchmark expansion

- Exact CP search, broad substring search, no-result search, and full dump.
- Installed versionless, versioned, repository, and null-delimited output.
- equery belongs/files/uses comparisons.
- Full and incremental index timing with result-count/digest checks.
- Single package and `@world` plans once their result normalization is defined.
- Memory and I/O profiling for the slow search path.
