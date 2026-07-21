# Configuration and state ownership

Arise interoperates with Portage without claiming Portage's configuration
namespace. The ownership boundary is:

- `/etc/portage` is shared Gentoo package policy. Arise reads it for profiles,
  repositories, package policy, toolchain settings, and familiar emerge
  behavior. Arise must not write Arise-only keys or formats there.
- `/etc/arise` contains administrator-managed Arise configuration, including
  scheduler policy, plan-store defaults, remote builders, recovery backends,
  tinderbox endpoints, and presentation settings.
- `/var/lib/arise` contains durable machine state, operation metadata, resume
  state, attestations, and other data that is neither configuration nor cache.
- `/var/cache/arise` contains rebuildable indexes, resolver snapshots, remote
  metadata, and other disposable caches.
- `/var/log/arise` contains Arise-native logs. Portage-compatible merge timing
  records may additionally be emitted to Portage's established log location so
  tools such as genlop and qlop continue to work.
- `/run/arise` contains process-local locks and runtime coordination state.
- `/var/tmp/arise` contains build trees, prepared package images, and durable
  temporary transaction material that must survive an ordinary reboot policy.

Files emitted into a Portage-owned namespace must use a format and meaning that
Portage itself accepts. If Arise needs richer data, it stores the canonical form
in an Arise-owned namespace and emits a deliberately degraded, compatible view
for existing ecosystem tools.

User-scoped configuration follows the same separation through the XDG base
directories and must not silently override system package policy with a second,
incompatible copy of `/etc/portage`.
