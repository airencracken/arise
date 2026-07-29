package planadapter

import (
	"strings"

	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/vdb"
)

// StateFromVDB converts independently scanned installed records into the
// validator's state model without implying repository metadata authority.
func StateFromVDB(packages []vdb.Package) planvalidate.State {
	result := planvalidate.State{Packages: make([]planvalidate.Package, len(packages))}
	for index, installed := range packages {
		iuse := make(map[string]bool, len(installed.IUse))
		use := make(map[string]bool, len(installed.IUse))
		for _, raw := range installed.IUse {
			flag := strings.TrimLeft(raw, "+-")
			if flag != "" {
				iuse[flag] = true
			}
		}
		for _, flag := range installed.Use {
			use[flag] = true
		}
		dependencies := map[string]string{
			"DEPEND": installed.Depend, "RDEPEND": installed.RDepend,
			"BDEPEND": installed.BDepend, "IDEPEND": installed.IDepend,
			"PDEPEND": installed.PDepend,
		}
		for class, expression := range dependencies {
			if strings.TrimSpace(expression) == "" {
				delete(dependencies, class)
			}
		}
		result.Packages[index] = planvalidate.Package{
			CPV: installed.CPV(), Slot: installed.Slot, Subslot: installed.Subslot,
			Repository: installed.Repository, Authority: planvalidate.AuthorityVDB,
			Use: use, IUse: iuse, Dependencies: dependencies, EAPI: installed.EAPI,
		}
	}
	return result
}
