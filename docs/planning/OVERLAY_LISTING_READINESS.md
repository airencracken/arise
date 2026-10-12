# Gentoo repository listing

Future listing is distinct from publishing a version to the existing overlay.
Do not prepare the Gentoo fork or submit a request without explicit instruction.

Before a listing request, refresh public cloneability, unique repo name,
`masters = gentoo`, source/feed URLs and versioned/live package metadata.
Align repository and package maintainer addresses with the Gentoo Bugzilla
account. Run `make check`, `pkgcheck scan` and the latest offline ebuild build;
retain revision and results. Historical checklist ticks are not current evidence.

The candidate [repository record](../../misc/arise-overlay-repositories.xml)
belongs in `gentoo/api-gentoo-org:files/overlays/repositories.xml`, with title
`repositories: add arise-overlay`. Follow Gentoo's
[official process](https://wiki.gentoo.org/wiki/Project:Overlays/Overlays_guide#Requesting_the_addition_of_an_overlay_via_Github_PRs).
