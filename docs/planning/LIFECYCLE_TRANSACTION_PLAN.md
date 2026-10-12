# Lifecycle mutation capture

Optional strengthening beyond ordinary Portage-compatible phase execution.
It must not be required by the default static binary or change credentials,
namespaces, network or sandbox policy. Required binary hook execution remains
separate unfinished work.

A Go/Linux syscall observer may stop filesystem mutations, resolve bounded
tracee path arguments and durably capture every affected preimage before
releasing the syscall. The rebuild transaction owns one journal from the first
ROOT mutation through payload/VDB commit. Relative paths, dirfds, both ends of
rename/link, writable mappings and fork/clone descendants require coverage.
Unknown syscalls, architectures, trace escapes, io_uring mutations or ambiguous
mount/path transitions fail before mutation.

Ptrace and seccomp notification are candidates, not certified backends. Promotion
requires immutable arguments: stop all relevant threads, safely emulate calls or
reject races. Notification validity, listener death, shared buffers and inherited
fds need adversarial tests. A notification alone does not eliminate TOCTOU.

Keep external tracing, overlay, FUSE and snapshot providers optional and
USE-gated. Test capture/sync/cancel/crash failures, hostile descendant/path races,
all ownership/mode/xattr/link restoration and enabled/disabled builds. Prove
preimage-before-mutation and ordinary isolation equivalence before promotion.
