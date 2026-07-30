# Native Arise equivalents for Gentoo query tools

Arise is a self-contained alternative with capabilities that overlap
`emerge`, `eix`, `equery`, `portageq`, and the `q` tools. It neither replaces
those tools nor clones their command names. They remain useful ecosystem peers
and differential references, but requiring one to diagnose package state or a
failed Arise operation is an Arise coverage bug.

Interoperability is a contract, not merely a migration convenience. Arise uses
the same Portage configuration, repositories, installed-package database,
world set, binary-package indexes, protected-configuration conventions, and
package atoms. Native line-oriented output and exit statuses are designed for
shell pipelines with the existing Gentoo tooling ecosystem. Arise-specific
formats may carry richer state, but they must retain an explicit import,
export, or canonical line-oriented interoperability path.

The native implementation is held to four additional requirements:

- it remains part of the portable static Arise binary;
- common queries avoid interpreter and Portage module startup;
- batch and JSON output are available where they improve diagnosis; and
- benchmarks compare equivalent live queries against the reference tools.

## Installed state

| Reference operation | Native Arise operation |
| --- | --- |
| `portageq match` | `arise installed --match ATOM` |
| `portageq has_version` | `arise installed --has ATOM` |
| `portageq best_version` | `arise installed --best ATOM` |
| `portageq mass_best_version` | `arise installed --best ATOM...` |
| `portageq contents`, `qlist`, `equery files` | `arise installed --contents ATOM` |
| `portageq owners`, `qfile`, `equery belongs` | `arise installed --owner PATH...` |
| `equery uses`, `quse` | `arise installed --uses ATOM` |
| `qsize`, `equery size` | `arise installed --size ATOM` |
| `qcheck`, `equery check` | `arise installed --check ATOM` |

## Repository and configuration state

| Reference operation | Native Arise operation |
| --- | --- |
| `portageq best_visible` | `arise query --best-visible [--type=ebuild\|binary\|installed] ATOM` |
| `portageq mass_best_visible` | `arise query --best-visible [--type=...] ATOM...` |
| `portageq all_best_visible` | `arise query --all-best-visible` |
| `portageq metadata` | `arise query --metadata=KEY,... [--type=ebuild\|binary\|installed] ATOM` |
| `portageq expand_virtual` | `arise query --expand-virtual ATOM` |
| `portageq pquery`, `eix`, `qsearch` | `arise search` and `arise query` |
| `portageq pquery --maintainer-email` | `arise search --search-maintainer EMAIL` |
| `portageq pquery --orphaned` | `arise search --search-orphaned` |
| `portageq get_repos` | `arise info --repositories` |
| `portageq get_repo_path` | `arise info --repo-path REPOSITORY...` |
| `portageq repositories_configuration` | `arise info --repository-config` |
| `portageq master_repositories` | `arise info --masters REPOSITORY...` |
| `portageq available_eclasses` | `arise info --eclasses REPOSITORY...` |
| `portageq eclass_path` | `arise info --eclass-path REPOSITORY ECLASS...` |
| `portageq license_path` | `arise info --license-path REPOSITORY LICENSE...` |
| `portageq envvar` and path queries | `arise info --value VARIABLE...` |
| `portageq distdir`, `pkgdir`, `portdir`, `portdir_overlay`, `vdb_path`, `gentoo_mirrors` | `arise info --value VARIABLE...` |
| `portageq colormap` | `arise info --colors` |
| `portageq config_protect` | `arise info --value CONFIG_PROTECT` |
| `portageq config_protect_mask` | `arise info --value CONFIG_PROTECT_MASK` |
| `portageq is_protected` | `arise info --is-protected PATH...` |
| `portageq filter_protected` | `arise info --filter-protected` |
| `portageq list_preserved_libs` | `arise info --preserved-libs` |
| `equery which` | `arise query --ebuild ATOM` |

`arise state json` and the JSON forms of search and resolution provide richer
machine-readable package state while canonical line output remains available
for pipelines.

Deprecated Portage aliases map to the same native operation. Portage's
`debug_signal`, `signal_interrupt`, and `uses_configroot` are implementation
hooks rather than package-state queries; Arise supplies its own cooperative
signal handling, diagnostic traps, and explicit `--portage-config-root`
boundary instead of exposing those names as commands.
