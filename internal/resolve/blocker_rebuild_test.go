package resolve

import (
	"fmt"
	"testing"
)

func TestResolveCompleteGraphDoesNotRebuildRemovedPerlCore(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "provider upgrade"
		if stale {
			name = "stale binding"
		}
		t.Run(name, func(t *testing.T) {
			g := makeGraph()
			pkg(g, "dev-lang/perl", "5.42.2", "0", "5.42", true, nil)
			pkg(g, "dev-lang/perl", "5.44.0", "0", "5.44", false, nil)
			core := pkg(g, "perl-core/Test-Harness", "3.500.0", "0", "0", true, nil)
			pkg(g, "perl-core/Test-Harness", "3.500.0", "0", "0", false, nil)
			core.EAPI, core.InstalledEAPI, core.DependencyMetadataKnown = "8", "8", true
			core.Rdepend = "dev-lang/perl:="
			core.InstalledRdepend = "dev-lang/perl:0/5.42="
			if stale {
				core.InstalledRdepend = "dev-lang/perl:0/5.40="
			}
			pkg(g, "virtual/perl-Test-Harness", "3.500.0-r1", "0", "0", true, nil)
			virtual := pkg(g, "virtual/perl-Test-Harness", "3.520.0", "0", "0", false, nil)
			virtual.EAPI, virtual.DependencyMetadataKnown = "8", true
			virtual.Rdepend = "|| ( =dev-lang/perl-5.44* ~perl-core/Test-Harness-3.520.0 ) dev-lang/perl:= !<perl-core/Test-Harness-3.520.0 !>perl-core/Test-Harness-3.520.0-r999"
			consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
			pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
			consumer.EAPI, consumer.InstalledEAPI, consumer.DependencyMetadataKnown = "8", "8", true
			consumer.Rdepend = "dev-lang/perl:= virtual/perl-Test-Harness"
			consumer.InstalledRdepend = "dev-lang/perl:0/5.42= virtual/perl-Test-Harness"
			// These valid alternatives cannot repair an invalid ABI rebuild. A
			// false blocker conflict used to replay unrelated world choices.
			consumer.Rdepend += " || ( app-misc/tool-a app-misc/tool-b )"
			consumer.InstalledRdepend += " || ( app-misc/tool-a app-misc/tool-b )"
			for _, cp := range []string{"app-misc/tool-a", "app-misc/tool-b"} {
				tool := pkg(g, cp, "1", "0", "0", true, nil)
				tool.DependencyMetadataKnown = true
			}

			cfg := DefaultResolveConfig()
			cfg.Update, cfg.Deep, cfg.NewUse, cfg.CompleteGraph = true, true, true, true
			cfg.Backtrack = 20
			cfg.WorldSet = &WorldSet{Entries: []string{"app-misc/consumer"}}
			result, err := Resolve(g, []string{"@world"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Verified || len(result.Conflicts) != 0 {
				t.Fatalf("Perl transition did not verify: %v", result.Conflicts)
			}
			if !hasAction(result.Uninstall, "perl-core/Test-Harness-3.500.0", "uninstall") || actionForCP(result.Install, "perl-core/Test-Harness") {
				t.Fatalf("blocked core must only be removed: installs=%v removals=%v", collectCPV(result.Install), collectCPV(result.Uninstall))
			}
			if !actionForCP(result.Install, "app-misc/consumer") {
				t.Fatal("retained Perl consumer was not rebuilt")
			}
			if result.BacktrackLevel != 0 || result.Metrics.ReplayBranches != 0 {
				t.Fatalf("valid transition retried unrelated choices: backtracks=%d replays=%d", result.BacktrackLevel, result.Metrics.ReplayBranches)
			}
		})
	}
}

func TestCompleteGraphRemovalMatchesExactConsumerIdentity(t *testing.T) {
	for _, stale := range []bool{false, true} {
		for _, removal := range []struct {
			name, version, repository string
			domain                    DependencyDomain
			wantRebuild               bool
		}{
			{"same instance", "1", "gentoo", DomainROOT, false},
			{"other version", "2", "gentoo", DomainROOT, true},
			{"other repository", "1", "overlay", DomainROOT, true},
			{"other root", "1", "gentoo", DomainBROOT, true},
		} {
			t.Run(fmt.Sprintf("stale=%t/%s", stale, removal.name), func(t *testing.T) {
				g := makeGraph()
				pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
				next := pkg(g, "dev-libs/provider", "2", "0", "2", false, nil)
				core := g.AddVersionFromRepository("app-misc/consumer", "1", "0", "0", true, nil, "amd64", "gentoo")
				core.Available, core.DependencyMetadataKnown = true, true
				core.EAPI, core.InstalledEAPI = "8", "8"
				core.Rdepend = "dev-libs/provider:="
				core.InstalledRdepend = "dev-libs/provider:0/1="
				if stale {
					core.InstalledRdepend = "dev-libs/provider:0/old="
				}
				removed := &PkgAction{Atom: mustParse("app-misc/consumer-" + removal.version), Repository: removal.repository, Domain: removal.domain, Action: "uninstall"}
				r := &resolver{
					graph: g, config: DefaultResolveConfig(),
					toInstall:   map[string]*PkgAction{versionActionKey("dev-libs/provider", next): {Atom: bestVersionAtom(g.Packages["dev-libs/provider"].Atom, next), Slot: "0", Action: "update"}},
					toUninstall: map[string]*PkgAction{actionVersionKey(removed): removed},
				}
				r.processCompleteGraph()
				rebuilt := r.toInstall[versionActionKey("app-misc/consumer", core)] != nil
				if rebuilt != removal.wantRebuild {
					t.Fatalf("consumer rebuilt=%t, want %t", rebuilt, removal.wantRebuild)
				}
			})
		}
	}
}
