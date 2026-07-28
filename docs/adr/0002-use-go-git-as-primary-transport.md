# ADR-0002: Use go-git as the primary Git transport

## Status

Accepted on 2026-07-28.

## Context

Arise historically used the system Git executable first and retained go-git as
a fallback. That arrangement made the dependency archive substantially larger:
go-git's OpenPGP graph includes Cloudflare CIRCL, whose module ZIP contains
large cryptographic test vectors. It also increased cold compile time and
binary size.

An experiment to move go-git behind a `rescue-git` USE flag measured the
opposite side of the tradeoff. The in-process implementation was substantially
faster than the multi-process system path on the maintained local
Git-protocol fixture. The system path repeatedly launched Git and rediscovered
repository state.

Before promotion, the built-in path lacked dirty-worktree refusal,
detached-HEAD refusal, atomic hard-reset behavior, and native ebuild change
reporting. Those behaviors were added and placed under shared contracts.

The transport benchmarks in
[`internal/sync/transport_benchmark_test.go`](../../internal/sync/transport_benchmark_test.go)
measure clone, no-op update, and incremental update over a real local Git
protocol server. At acceptance on an AMD Ryzen 7 PRO 4750U, five-iteration
medians were:

| Operation | go-git | System Git |
|---|---:|---:|
| Clone | 15.0 ms | 120.5 ms |
| No-op update | 14.6 ms | 133.8 ms |
| Incremental update | 23.7 ms | 140.5 ms |

These measurements establish subprocess-sensitive behavior; they do not claim
the same ratio for a full Gentoo repository over the public network.
`-benchmem` reports more allocations in the Arise process for go-git, as
expected for an in-process implementation. The system figures exclude memory
allocated by the child Git process and therefore are not a whole-operation
memory comparison.

[`internal/sync/transport_contract_test.go`](../../internal/sync/transport_contract_test.go)
proves primary/fallback ordering and prevents the fallback from bypassing
safety policy. The broader sync tests cover dirty and detached repositories,
change reporting, cancellation, and update atomicity.

## Decision

Use go-git as Arise's primary implementation for repository origin discovery,
clone, fetch, update, hard reset, tree comparison, and package-version change
reporting.

Retain the system Git executable as a compatibility fallback only after a
built-in transport failure. Do not invoke the fallback for safety-policy
failures such as a dirty worktree or detached HEAD. Use rsync only for
repositories explicitly configured with `sync-type = rsync`.

## Consequences

- Arise accepts the larger module archive, binary, and cold compile cost because
  the dependency provides a measured runtime advantage and a Git-capable
  recovery path without requiring the system executable.
- Repository synchronization avoids subprocess overhead in the normal case.
- System Git preserves compatibility with remotes or authentication behavior
  unsupported by go-git, at the cost of maintaining a narrower secondary path.
- Upgrades to go-git and its cryptographic dependency graph require normal
  license, vulnerability, archive-size, offline-build, and performance review.
- The system fallback must never weaken built-in atomicity or safety policy.

A superseding ADR is warranted if representative Gentoo-sized and remote-network
benchmarks reverse the result, go-git cannot support required authentication or
protocol behavior, its dependency or security cost becomes unacceptable, or a
single optimized system-Git invocation matches the in-process implementation
without weakening recovery behavior.
