# Execution isolation backends

Arise defaults to Gentoo's `sandbox`, matching the installed Portage execution
model and preserving recovery-system compatibility. It does not silently run a
phase with a missing requested backend.

Portage combines several independent mechanisms:

- `sandbox`/`usersandbox` enforce and report filesystem access policy;
- `userpriv` and `userfetch` drop privileges for eligible work;
- `network-sandbox`, `ipc-sandbox`, `mount-sandbox`, and `pid-sandbox` use
  direct Linux namespace setup;
- phase, RESTRICT, and PROPERTIES policy decides when individual boundaries are
  intentionally relaxed.

Arise will reproduce those mechanisms directly in its default backend. Each
namespace is capability-detected independently: when the kernel, policy, or
current privilege context rejects one, Arise will emit a prominent diagnostic
and continue with the remaining Portage-style protections. In particular, the
default path will not require unprivileged user namespaces. This keeps the Go
control plane independent of Python and avoids making Bubblewrap a recovery
dependency.

Bubblewrap remains an optional enhanced backend. It provides a smaller readable
filesystem and convenient namespace assembly, but depends on an external
binary and unprivileged-user-namespace policy. It must therefore be selected
explicitly (eventually via a build/runtime USE-controlled capability), and
failure never silently falls back to a weaker backend. The caller may explicitly
retry with the Portage-compatible backend after seeing the failure.

Both backends use the same versioned phase protocol, environment contract,
structured logs, lifecycle semantics, and post-phase verification. Backend
choice may strengthen containment but may not change package results.
