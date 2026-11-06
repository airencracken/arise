package planadapter

import (
	"strings"

	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/vdb"
)

// InstalledState freezes freshly scanned VDB metadata for locked plan replay.
// Repository dependencies must never substitute for these installed bindings.
func InstalledState(packages []vdb.Package) []planvalidate.Package {
	state := make([]planvalidate.Package, 0, len(packages))
	for _, installed := range packages {
		use := make(map[string]bool, len(installed.Use))
		for _, flag := range installed.Use {
			use[flag] = true
		}
		state = append(state, planvalidate.Package{
			CPV: installed.CPV(), Slot: installed.Slot, Subslot: installed.Subslot,
			Repository: installed.Repository, Authority: planvalidate.AuthorityVDB,
			Use: use, IUse: parseIUse(strings.Join(installed.IUse, " ")),
			EAPI: installed.EAPI, RequiredUse: installed.RequiredUse,
			Dependencies: map[string]string{
				"DEPEND": installed.Depend, "RDEPEND": installed.RDepend,
				"BDEPEND": installed.BDepend, "IDEPEND": installed.IDepend,
				"PDEPEND": installed.PDepend,
			},
		})
	}
	return state
}
