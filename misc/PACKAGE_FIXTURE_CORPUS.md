# Package fixture corpus

Signal remains a binary-only, large-job-count and user-patch regression, not a
representative resolver or execution benchmark. The
additional corpus is selected by distinct package-manager behavior; adding more
packages with the same behavior is lower value than deepening these fixtures.

## Initial single-package corpus

- `app-misc/hello`: tiny non-installed source/autotools baseline suitable for
  fast fetch, unpack, default phase, image-tree and merge rehearsal tests.
- `app-portage/golop`: small installed Go package and prior art for a fully
  static recovery tool; exercises Go source supply without Arise's own graph.
- `app-portage/emlop`: installed Rust package; exercises Cargo source/vendor
  policy and provides emerge-log behavioral comparisons.
- `sys-apps/baselayout`: installed config-protected package; primary
  CONFIG_PROTECT, dispatch-conf/etc-update and transaction fixture.
- `app-shells/bash`: ordinary source package with patches, configure/make and
  broad dependency policy; a realistic default-phase step above hello.
- `virtual/libcrypt`: installed virtual/provider fixture with essentially no
  build noise, useful for resolver semantics independently of execution.
- `dev-lang/python`: interpreter slot/target transition and broken-control-plane
  recovery fixture. Never make its current damaged state the only Python case;
  retain a clean synthetic companion root.
- `sys-apps/portage`: self-hosting recovery target proving Arise can repair the
  Python package-manager stack without invoking it for execution.
- `sys-libs/glibc`: subslot/ABI, preserve-libs and critical transaction fixture;
  use disposable roots only until P6 recovery is complete.
- `sys-devel/gcc`: multi-slot toolchain, bootstrap/build-root and resource
  scheduling fixture; not part of the fast suite.
- `dev-lang/rust-bin`: binary archive and large installed toolchain fixture,
  complementary to source-built emlop.
- `sys-kernel/gentoo-sources`: very large fetch/unpack, user patches and kernel
  configuration fixture; explicitly slow and excluded from routine CI.

Record exact CPV, repository commit, profile/config/VDB fingerprint, effective
USE and Portage plan with every promoted fixture. Classify cases as fast,
integration, slow, destructive-root rehearsal or live read-only; never let a
slow package silently enter the fast lane.
