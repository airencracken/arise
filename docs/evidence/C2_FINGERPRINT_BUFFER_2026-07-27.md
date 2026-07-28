# C2 state-fingerprint buffer optimization

## Verdict

Accepted. Reusing one bounded copy buffer across the state-fingerprint walk
reduced median live deep/newuse wall time by 8.4%, combined CPU time by 33.9%,
profiled allocation by 53.3%, and median peak RSS by 2.3%. The verified plan,
plan digest, and state digest were unchanged.

This is the consolidation cycle's one accepted optimization.

## Workload and identity

- baseline source: `d5f4f6360337eb7ddaba7565efdaa89bc387d909` plus the
  documentation, benchmark-instrumentation, and command-free rollback
  prototype changes in the current worktree
- date: 2026-07-27
- CPU: AMD Ryzen 7 PRO 4750U
- operation: read-only live deep/newuse `@world` update
- cache state: ordinary warm host state; no page-cache drop
- Arise exit status: zero for every run
- resolver result: whole-state verified for every run
- plan SHA-256:
  `143d194ae78ba3a9b1a6f7517051be06087808345367ee7f37d43a1a081afef4`
- state SHA-256:
  `4c7a0233cce4d97ceec98ffc67c2fb65978b6cc4b87402a35691e8f67806c8e7`

Command shape:

```sh
/usr/bin/time -f '%e %U %S %M' arise \
  --pretend --update --deep --newuse --complete-graph \
  --with-bdeps=y --keep-going --backtrack=20 \
  --resolver-timeout=5m --json install @world
```

The baseline has three repetitions and the candidate has five. The normalized
action/conflict/warning projection was identical across all eight runs.

## Profile diagnosis

The baseline Go allocation profile reported 2,645.55 MiB total allocation.
`mutationStateSHA256` accounted for 947.51 MiB, of which 921.20 MiB came from
`io.copyBuffer`. `hashStatePath` called `io.Copy` separately for every regular
VDB, Portage configuration, repository package, and eclass file. The generic
file-to-hash path allocated a fresh buffer for each file.

The CPU profile attributed 51.1% of all samples to background garbage
collection and 43.6% cumulatively to span scanning. State hashing itself
accounted for 7.4% cumulative CPU, so reducing its allocation also removed
substantial GC work elsewhere in the process.

The accepted implementation hides the file's `WriterTo` fast path and calls
`io.CopyBuffer` with one lazily allocated 128 KiB buffer shared by the
sequential tree walk. Hash input ordering, metadata encoding, file bytes,
symlink handling, error propagation, and missing-path behavior are unchanged.

## Repeated results

Times are seconds. Peak RSS is `/usr/bin/time` maximum resident set size in
KiB.

### Baseline

| run | wall | user | system | CPU total | peak RSS KiB |
|---:|---:|---:|---:|---:|---:|
| 1 | 3.88 | 7.22 | 0.90 | 8.12 | 946,164 |
| 2 | 3.45 | 7.09 | 0.63 | 7.72 | 811,288 |
| 3 | 3.41 | 6.76 | 0.66 | 7.42 | 812,612 |
| median | 3.45 | | | 7.72 | 812,612 |

### Accepted candidate

| run | wall | user | system | CPU total | peak RSS KiB |
|---:|---:|---:|---:|---:|---:|
| 1 | 3.18 | 4.38 | 0.58 | 4.96 | 919,048 |
| 2 | 3.23 | 4.63 | 0.62 | 5.25 | 1,010,848 |
| 3 | 3.16 | 4.59 | 0.54 | 5.13 | 794,148 |
| 4 | 3.15 | 4.39 | 0.55 | 4.94 | 776,408 |
| 5 | 3.16 | 4.57 | 0.53 | 5.10 | 773,620 |
| median | 3.16 | | | 5.10 | 794,148 |

The optimized allocation profile reported 1,236.25 MiB total allocation.
`io.copyBuffer` no longer appeared among the leading owners.

## Tests and safety gates

- canonical fingerprint-stream test over directories and regular files;
- injected writer-failure propagation with a file larger than the shared
  buffer;
- existing mutation tests for policy, VDB, environment and plan binding;
- identical live normalized plan, plan digest and state digest;
- full command package and repository test suites;
- race, vet, adversarial, mutation-contract and formatting gates.

## Deferred findings

The remaining profile is led by VDB metadata reads, dependency graph
construction, atom parsing, dependency metadata collection, decoded resolver
snapshot strings, and effective USE calculation. The consolidation plan stops
after this accepted optimization. Those owners should seed the next bounded
performance cycle rather than being folded into this change.
