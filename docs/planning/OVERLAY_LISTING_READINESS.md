# Gentoo overlay listing readiness

This checklist prepares `airencracken/arise-overlay` for addition to Gentoo's
global repository list. It does not authorize or submit the request.

The authoritative process is Gentoo's
[Overlays guide](https://wiki.gentoo.org/wiki/Project:Overlays/Overlays_guide#Requesting_the_addition_of_an_overlay_via_Github_PRs).
The eventual change belongs in
`gentoo/api-gentoo-org:files/overlays/repositories.xml`.

## Current audit

Audited overlay revision:
`5fb37414605d7f869753f714706c616e26e495af`.

- [x] The repository is publicly cloneable over Git and HTTPS.
- [x] `profiles/repo_name` contains the unique name `arise-overlay`.
- [x] `metadata/layout.conf` declares `masters = gentoo`.
- [x] The GitHub source, SSH source, homepage and Atom feed URLs are defined.
- [x] The overlay's own `make check` passes.
- [x] Versioned and live ebuilds, package metadata, Manifest and md5-cache
  entries are present.
- [x] The proposed owner address matches the maintainer's Gentoo Bugzilla
  account. Gentoo verifies this submitted owner address; GitHub's public
  no-reply address is intentionally not used.
- [ ] Align the overlay package's `metadata.xml` maintainer address with
  the Bugzilla account before requesting listing, so package and repository
  ownership expose one consistent contact.
- [ ] Run `pkgcheck scan` from the overlay with `pkgcheck` installed and resolve
  every error. The current audit host lacks `pkgcheck`; `make check` reported
  that repository QA was skipped.
- [ ] Re-run an offline build of the latest versioned ebuild from a clean
  Portage state and retain the result with the eventual request.

The proposed standalone record is
[`../../misc/arise-overlay-repositories.xml`](../../misc/arise-overlay-repositories.xml).
At request time, copy its single `repo` element into alphabetical order in the
Gentoo file.

## Request procedure

Only after every item above is complete:

1. Pull the current `gentoo/api-gentoo-org` master branch.
2. Insert the tested `repo` element into
   `files/overlays/repositories.xml` in repository-name order.
3. Run `make check`; this requires `dev-libs/libxml2`.
4. Commit only that file with
   `repositories: add arise-overlay`.
5. Open the GitHub pull request.

Do not prepare the Gentoo fork or open the request until listing is explicitly
authorized.
