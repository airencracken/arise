package resolve

import "testing"

func TestVerifyTransactionRejectsOmittedConsumerRepair(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
	next := pkg(g, "dev-libs/provider", "2", "0", "2", false, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.Available, consumer.DependencyMetadataKnown = true, true
	consumer.EAPI, consumer.InstalledEAPI = "8", "8"
	consumer.Rdepend = "dev-libs/provider:="
	consumer.InstalledRdepend = "dev-libs/provider:0/1="
	cfg := DefaultResolveConfig()
	cfg.CompleteGraph = true
	installs := []PkgAction{{Atom: bestVersionAtom(g.Packages["dev-libs/provider"].Atom, next), Slot: "0", Subslot: "2", Action: "update"}}
	result, err := VerifyTransaction(g, installs, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verified || len(result.Conflicts) == 0 {
		t.Fatalf("transaction omitting an ABI consumer repair was accepted: %#v", result)
	}
	if len(result.Install) != len(installs) {
		t.Fatal("verification changed the supplied transaction")
	}
}

func TestVerifyTransactionRejectsUnknownInstallIdentity(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	for _, cpv := range []string{"app-misc/consumer-2", "app-misc/absent-1"} {
		t.Run(cpv, func(t *testing.T) {
			result, err := VerifyTransaction(g, []PkgAction{{Atom: mustParse(cpv), Slot: "0", Action: "install"}}, nil, DefaultResolveConfig())
			if err == nil && result.Verified {
				t.Fatal("unknown install identity was certified")
			}
		})
	}
}

func TestVerifyTransactionCompletesUniqueIdentityWithoutChangingInput(t *testing.T) {
	g := makeGraph()
	version := g.AddVersionFromRepository("app-misc/consumer", "1", "0", "abi", false, nil, "amd64", "gentoo")
	version.EAPI, version.DependencyMetadataKnown = "8", true
	installs := []PkgAction{{Atom: mustParse("app-misc/consumer-1"), Action: "install"}}
	result, err := VerifyTransaction(g, installs, nil, DefaultResolveConfig())
	if err != nil || !result.Verified {
		t.Fatalf("known install was rejected: %#v, %v", result, err)
	}
	if installs[0].Slot != "" || installs[0].Subslot != "" || installs[0].Repository != "" {
		t.Fatal("verification mutated the caller's input")
	}
	if result.Install[0].Slot != "0" || result.Install[0].Subslot != "abi" || result.Install[0].Repository != "gentoo" {
		t.Fatalf("verified identity was not completed: %#v", result.Install)
	}
	g.AddVersionFromRepository("app-misc/consumer", "1", "0", "abi", false, nil, "amd64", "overlay")
	if _, err := VerifyTransaction(g, installs, nil, DefaultResolveConfig()); err == nil {
		t.Fatal("ambiguous repository identity was accepted")
	}
	for _, test := range []struct {
		name, target, slot, subslot, repository string
	}{
		{"wrong repository", "app-misc/consumer-1", "", "", "missing"},
		{"wrong slot", "app-misc/consumer-1", "2", "", "gentoo"},
		{"wrong subslot", "app-misc/consumer-1", "0", "wrong", "gentoo"},
		{"wrong atom subslot", "app-misc/consumer-1:0/wrong::gentoo", "", "", ""},
		{"missing version", "app-misc/consumer", "", "", "gentoo"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyTransaction(g, []PkgAction{{Atom: mustParse(test.target), Slot: test.slot, Subslot: test.subslot, Repository: test.repository}}, nil, DefaultResolveConfig()); err == nil {
				t.Fatal("invalid transaction identity was accepted")
			}
		})
	}
}
