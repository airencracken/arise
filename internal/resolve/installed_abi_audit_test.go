package resolve

import (
	"fmt"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestInstalledABIInspectionKeepsBindingsOfScheduledConsumer(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.Available, consumer.DependencyMetadataKnown = true, true
	consumer.EAPI, consumer.InstalledEAPI = "8", "8"
	consumer.Rdepend = "dev-libs/provider:="
	consumer.InstalledRdepend = "dev-libs/provider:0/1="
	r := &resolver{graph: g, config: DefaultResolveConfig(), toInstall: map[string]*PkgAction{
		versionActionKey("app-misc/consumer", consumer): {Atom: bestVersionAtom(g.Packages["app-misc/consumer"].Atom, consumer), Slot: "0", Action: "reinstall"},
	}}
	dependents := r.installedSlotOperatorDependents("dev-libs/provider", "0")
	if len(dependents) != 1 || dependents[0].node.Atom.CP() != "app-misc/consumer" {
		t.Fatalf("scheduled rebuild hid the installed ABI binding: %#v", dependents)
	}
}

func BenchmarkInstalledSlotOperatorDependents(b *testing.B) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
	for index := range 300 {
		consumer := pkg(g, fmt.Sprintf("app-misc/consumer%d", index), "1", "0", "0", true, nil)
		consumer.Available, consumer.DependencyMetadataKnown = true, true
		consumer.EAPI, consumer.InstalledEAPI = "8", "8"
		consumer.Rdepend = "dev-libs/provider:="
		consumer.InstalledRdepend = "dev-libs/provider:0/1="
	}
	policy := &portage.Config{}
	for index := range 1000 {
		policy.PackageUseRules = append(policy.PackageUseRules, portage.PackageUseRule{
			Atom: fmt.Sprintf("app-misc/other%d", index), Flags: []string{"feature"},
		})
	}
	r := &resolver{graph: g, config: DefaultResolveConfig(), portageConfig: policy}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if dependents := r.installedSlotOperatorDependents("dev-libs/provider", "0"); len(dependents) != 300 {
			b.Fatalf("dependents=%d, want 300", len(dependents))
		}
	}
}
