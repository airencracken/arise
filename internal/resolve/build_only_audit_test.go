package resolve

import "testing"

func TestBuildOnlyDoesNotRebuildConsumersOfAnUninstalledArchive(t *testing.T) {
	g := makeGraph()
	old := pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
	old.Available = true
	next := pkg(g, "dev-libs/provider", "2", "0", "2", false, nil)
	next.DependencyMetadataKnown = true
	consumer := pkg(g, "app-misc/client", "1", "0", "0", true, nil)
	consumer.InstalledEAPI = "8"
	consumer.InstalledRdepend = "dev-libs/provider:0/1="
	consumer.Rdepend = "dev-libs/provider:="
	consumer.Available = true
	consumer.DependencyMetadataKnown = true
	result, err := Resolve(g, []string{"=dev-libs/provider-2"}, ResolveConfig{BuildPkgOnly: true, Update: true, CompleteGraph: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Conflicts) != 0 {
		t.Fatalf("archive broke retained dependency: %#v", result.Conflicts)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "app-misc/client" {
			t.Fatal("archive scheduled unnecessary consumer rebuild")
		}
	}
}
