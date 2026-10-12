package resolve

import "testing"

func TestSlotOperatorProviderUpdateKeepsDependencyDomain(t *testing.T) {
	for _, domain := range []DependencyDomain{DomainBROOT, DomainSYSROOT} {
		for _, ignore := range []string{"", "y"} {
			t.Run(string(domain)+"/ignore="+ignore, func(t *testing.T) {
				g := makeGraph()
				pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
				pkg(g, "dev-libs/provider", "2", "0", "2", false, nil)
				consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
				consumer.Available, consumer.DependencyMetadataKnown = true, true
				consumer.EAPI, consumer.InstalledEAPI = "8", "8"
				if domain == DomainBROOT {
					consumer.Bdepend, consumer.InstalledBdepend = "dev-libs/provider:=", "dev-libs/provider"
				} else {
					consumer.Depend, consumer.InstalledDepend = "dev-libs/provider:=", "dev-libs/provider"
				}
				installed := makeGraph()
				pkg(installed, "dev-libs/provider", "1", "0", "1", true, nil)
				cfg := DefaultResolveConfig()
				cfg.Update, cfg.Deep = true, true
				cfg.IgnoreBuiltSlotOperatorDeps = ignore
				cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{domain: installed}
				result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if !result.Verified || len(result.Install) != 1 || result.Install[0].Atom.CPV() != "dev-libs/provider-2" || result.Install[0].Domain != domain {
					t.Fatalf("provider update escaped %s: actions=%#v conflicts=%v", domain, result.Install, result.Conflicts)
				}
			})
		}
	}
}
