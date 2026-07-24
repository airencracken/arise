# Pre-commit lifecycle transaction plan

## Goal

Run `pkg_setup` and `pkg_preinst` with Portage-compatible phase policy while
durably capturing every filesystem preimage before the corresponding mutation.
This is an optional strengthening feature, not an execution-policy gate for
the current `-uDN @world` plan. Portage does not transactionally capture
arbitrary writes from these free lifecycle phases, so the default Arise lane
must execute them with Portage-compatible policy even when this feature is
absent. Bubblewrap, overlayfs, FUSE and filesystem snapshots are likewise
optional strengthening layers.

## Baseline backend

The optional dependency-free backend is a Linux syscall observer implemented
in the static Go binary.
It starts the ordinary Portage-compatible worker command under tracing and
follows every fork, vfork and clone descendant. At each filesystem-mutating
syscall entry it must:

1. stop the calling task before the kernel performs the mutation;
2. copy pathname arguments from tracee memory with bounded reads;
3. resolve relative paths against the tracee's `/proc/<pid>/cwd` or
   `/proc/<pid>/fd/<dirfd>`, preserving kernel `*at` semantics;
4. reject paths that cannot be resolved unambiguously;
5. durably call `journal.Capture` for every affected preimage; and
6. resume the syscall only after capture succeeds.

The observer does not change credentials, mount visibility, network, IPC, PID
or sandbox allow/deny policy. Portage isolation remains responsible for those
semantics. The observer supplies ordering and durability, not isolation.

### Seccomp integration

`SECCOMP_RET_USER_NOTIF` is a candidate selective-notification backend. A
filter installed with `SECCOMP_FILTER_FLAG_NEW_LISTENER` can notify the static
Arise supervisor only for filesystem-mutating syscalls instead of stopping at
every syscall as ptrace does. Availability and the exact notification/filter
features must be probed at runtime; kernels without them fall back to the
ptrace backend or fail closed when neither certified backend is available.

Seccomp is a Linux kernel interface, not an external helper or linkage
dependency, so it does not violate the standalone-static contract and does not
itself require a packaging USE flag. An optional userspace seccomp library
would require a fallback and USE-controlled packaging, but the baseline must
use direct Go/Linux interfaces.

User notification is not automatically race-free. `CONTINUE` stops only the
notifying thread; another thread or shared-memory writer can change a pathname
buffer after Arise reads it but before the kernel consumes it. The ptrace
backend has the same risk if it stops only the calling thread. Promotion
therefore requires one of:

- a proven stop-the-world protocol covering every tracee thread while path
  arguments are copied, captured and released into the syscall;
- safe supervisor-side emulation of the syscall, including credentials,
  namespaces, dirfds and returned descriptors (`SECCOMP_IOCTL_NOTIF_ADDFD`
  where applicable); or
- rejection of calls whose arguments cannot be made immutable.

`SECCOMP_IOCTL_NOTIF_ID_VALID`, bounded tracee-memory reads, TSYNC behavior,
listener death, cancellation and notification-fd inheritance all require
adversarial tests. Seccomp may be selected for performance only after it
provides the same preimage-before-mutation guarantee as ptrace.

## Required syscall coverage

The first supported Linux architecture must cover and adversarially test all
available variants of:

- write-capable `open`, `openat`, `openat2` and `creat`;
- `truncate`, metadata/time, ownership, mode and xattr mutation;
- `mkdir`, `mknod`, FIFO and symlink creation;
- `link`, `rename`, removal and their `*at` variants;
- writable memory mappings whose backing file was not already captured; and
- namespace/mount operations that could redirect later path resolution.

Rename and link operations have two relevant paths and must capture both.
An unrecognized syscall, ABI/personality, path encoding, trace escape, detached
descendant, `io_uring` filesystem mutation, or unsupported mount-namespace
transition fails the phase before that operation is permitted. Architecture
support is explicit; unsupported architectures retain resolution, inspection
and staged-root behavior but reject write-capable live lifecycle phases.

## Journal and lock ownership

The rebuild transaction, not `merge`, must own the journal. It begins before
the first traced pre-commit lifecycle phase and is passed into merge for
payload/VDB capture and the final commit. Any setup, build, preinstall or merge
failure rolls the same journal back. Recovery can reopen the active journal
after process or machine failure.

The live operation lock must exclude competing Arise mutations for the entire
active-journal window. External filesystem writers cannot be silently assumed
away: immediately before each traced mutation, capture must verify that the
preimage it records is the one visible to the stopped tracee. Payload merge
retains its existing batched capture/revalidation rules.

Long source builds should occur before opening the live journal whenever their
`pkg_setup` did not mutate ROOT. The observer therefore records whether a phase
targeted ROOT. A read-only setup can finish and release tracing without opening
an empty long-lived journal; the first actual ROOT mutation synchronously opens
the journal under the operation lock before it is allowed.

## Static and optional-feature contract

The observer uses Go plus Linux kernel interfaces and adds no shared-library or
helper-program dependency, but it exceeds Portage's baseline guarantees and
therefore must be enabled by an explicit ebuild USE flag. Optional
snapshot, overlay, FUSE, bubblewrap or external tracing providers must have a
baseline fallback and remain behind explicit packaging USE flags as additional
enhancements.

## Promotion tests

- Every covered syscall mutates an existing and an absent target, then rollback
  restores byte content, type, link target, ownership, mode, timestamps and
  xattrs.
- Fork/clone grandchildren and static executables cannot escape observation.
- Relative paths, dirfds, symlink ancestors, rename races and namespace changes
  fail closed or capture the correct target.
- Multithreaded path-buffer races are exercised against both ptrace and seccomp
  notification backends; the observed target and the kernel-mutated target can
  never differ.
- Injected capture, sync, worker, cancellation and crash failures never permit
  the stopped syscall before its preimage record is durable.
- A hostile ebuild fixture attempts every supported mutation from `pkg_setup`
  and `pkg_preinst`; successful merge commits one journal, while every injected
  failure rolls back that same journal.
- The normal Portage isolation differential remains unchanged with lifecycle
  tracing enabled.
- Both USE-disabled Portage-compatible execution and USE-enabled strengthened
  execution have independent build/runtime tests. The current static binary
  must pass all 273 actions in `--preflight-only -uDN @world` without requiring
  the optional feature.
