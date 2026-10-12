package resolve

import "testing"

func TestGraphNormalizesCombinedSlotMetadata(t *testing.T) {
	g := makeGraph()
	version := pkg(g, "dev-libs/provider", "1", "0/abi", "", false, nil)
	if version.Slot != "0" || version.Subslot != "abi" || len(g.Packages["dev-libs/provider"].Slots["0"]) != 1 {
		t.Fatalf("combined slot metadata was not normalized: %#v", version)
	}
}

func TestCompleteGraphNewProviderSlotDoesNotRebuildRetainedSlotConsumer(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "1", "1", true, nil)
	pkg(g, "dev-libs/provider", "2", "2", "2", false, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.Available, consumer.DependencyMetadataKnown = true, true
	consumer.EAPI, consumer.InstalledEAPI = "8", "8"
	consumer.Rdepend, consumer.InstalledRdepend = "dev-libs/provider:1=", "dev-libs/provider:1/1="
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.CompleteGraph = true, true
	result, err := Resolve(g, []string{"dev-libs/provider:2"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Install) != 1 || result.Install[0].Atom.CPV() != "dev-libs/provider-2" {
		t.Fatalf("new parallel slot rebuilt an unaffected consumer: actions=%v conflicts=%v", collectCPV(result.Install), result.Conflicts)
	}
}

func TestCompleteGraphRepairsStaleBindingInLowerConsumerSlot(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "2", "0", "current", true, nil)
	for _, slot := range []string{"1", "2"} {
		consumer := pkg(g, "app-misc/consumer", slot, slot, "0", true, nil)
		consumer.Available, consumer.DependencyMetadataKnown = true, true
		consumer.EAPI, consumer.InstalledEAPI = "8", "8"
		consumer.Rdepend = "dev-libs/provider:="
		consumer.InstalledRdepend = "dev-libs/provider:0/current="
		if slot == "1" {
			consumer.InstalledRdepend = "dev-libs/provider:0/old="
		}
	}
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.CompleteGraph = true, true
	result, err := Resolve(g, []string{"app-misc/consumer:1"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || !hasAction(result.Install, "app-misc/consumer-1", "reinstall") || hasAction(result.Install, "app-misc/consumer-2", "reinstall") {
		t.Fatalf("stale lower slot was missed or current slot rebuilt: actions=%v conflicts=%v", collectCPV(result.Install), result.Conflicts)
	}
}

func TestCompleteGraphForeignRootProviderDoesNotRebuildRootConsumer(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "old", true, nil)
	next := pkg(g, "dev-libs/provider", "2", "0", "new", false, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.Available, consumer.DependencyMetadataKnown = true, true
	consumer.EAPI, consumer.InstalledEAPI = "8", "8"
	consumer.Rdepend, consumer.InstalledRdepend = "dev-libs/provider:=", "dev-libs/provider:0/old="
	r := &resolver{graph: g, config: DefaultResolveConfig(), toInstall: map[string]*PkgAction{
		domainActionKey(versionActionKey("dev-libs/provider", next), DomainBROOT): {Atom: bestVersionAtom(g.Packages["dev-libs/provider"].Atom, next), Slot: "0", Domain: DomainBROOT, Action: "update"},
	}}
	r.processCompleteGraph()
	if r.packageScheduled(g.Packages["app-misc/consumer"]) {
		t.Fatal("BROOT provider update rebuilt ROOT consumer")
	}
}

func TestCompleteGraphUpdatesEachProviderAndConsumerSlot(t *testing.T) {
	for _, targets := range [][]string{
		{"dev-libs/provider:1"},
		{"dev-libs/provider:1", "dev-libs/provider:2"},
	} {
		t.Run(targets[len(targets)-1], func(t *testing.T) {
			g := makeGraph()
			pkg(g, "dev-libs/provider", "1", "1", "old1", true, nil)
			pkg(g, "dev-libs/provider", "1.1", "1", "new1", false, nil)
			pkg(g, "dev-libs/provider", "2", "2", "old2", true, nil)
			pkg(g, "dev-libs/provider", "2.1", "2", "new2", false, nil)
			for _, slot := range []string{"1", "2"} {
				consumer := pkg(g, "app-misc/consumer", slot, slot, "0", true, nil)
				consumer.Available, consumer.DependencyMetadataKnown = true, true
				consumer.EAPI, consumer.InstalledEAPI = "8", "8"
				consumer.Rdepend = "dev-libs/provider:" + slot + "="
				consumer.InstalledRdepend = "dev-libs/provider:" + slot + "/old" + slot + "="
			}
			cfg := DefaultResolveConfig()
			cfg.Update, cfg.CompleteGraph = true, true
			result, err := Resolve(g, targets, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Verified || !hasAction(result.Install, "app-misc/consumer-1", "reinstall") {
				t.Fatalf("slot 1 consumer was not rebuilt: actions=%v conflicts=%v", collectCPV(result.Install), result.Conflicts)
			}
			if rebuilt := hasAction(result.Install, "app-misc/consumer-2", "reinstall"); rebuilt != (len(targets) == 2) {
				t.Fatalf("slot 2 rebuilt=%t, want %t: actions=%v", rebuilt, len(targets) == 2, collectCPV(result.Install))
			}
		})
	}
}
