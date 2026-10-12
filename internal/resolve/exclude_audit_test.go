package resolve

import "testing"

func TestResolveExcludeRetainsInstalledWorldPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/provider", "2", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep, cfg.CompleteGraph = true, true, true
	cfg.WorldSet = &WorldSet{Entries: []string{"dev-libs/provider"}}
	cfg.Exclude = []string{"dev-libs/provider"}
	result, err := Resolve(g, []string{"@world"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Install) != 0 || len(result.Uninstall) != 0 {
		t.Fatalf("excluded installed world package was changed: %#v", result)
	}
}

func TestResolveExcludeRequiredDependencyCannotCertifyUpdate(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/provider", "2", "0", "0", false, nil)
	consumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, nil)
	consumer.Rdepend = ">=dev-libs/provider-2"
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep, cfg.CompleteGraph = true, true, true
	cfg.Exclude = []string{"dev-libs/provider"}
	result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
	if err == nil && result.Verified {
		t.Fatal("required excluded dependency was silently updated")
	}
	if result != nil && actionForCP(result.Install, "dev-libs/provider") {
		t.Fatal("failed plan still contains an excluded dependency update")
	}
}

func TestResolveExcludePreventsSameVersionRebuild(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "app-misc/consumer", "1", "0", "0", true, map[string]bool{"feature": false})
	installed.Available = true
	installed.UseFlags = map[string]bool{"feature": true}
	cfg := DefaultResolveConfig()
	cfg.NewUse, cfg.Reinstall, cfg.Deep = true, true, true
	cfg.Exclude = []string{"app-misc/consumer"}
	result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
	if err != nil || !result.Verified || len(result.Install) != 0 {
		t.Fatalf("excluded same-version package was rebuilt: result=%#v err=%v", result, err)
	}
}

func TestResolveExcludeHonorsVersionAndRepository(t *testing.T) {
	for _, test := range []struct {
		name, excluded, wantVersion string
	}{
		{"version range", ">=dev-libs/provider-2", ""},
		{"repository", "dev-libs/provider::overlay", "2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := makeGraph()
			g.AddVersionFromRepository("dev-libs/provider", "1", "0", "0", true, nil, "amd64", "gentoo")
			g.AddVersionFromRepository("dev-libs/provider", "2", "0", "0", false, nil, "amd64", "gentoo")
			g.AddVersionFromRepository("dev-libs/provider", "3", "0", "0", false, nil, "amd64", "overlay")
			cfg := DefaultResolveConfig()
			cfg.Update = true
			cfg.Exclude = []string{test.excluded}
			result, err := Resolve(g, []string{"dev-libs/provider"}, cfg)
			if err != nil || !result.Verified {
				t.Fatalf("resolution failed: result=%#v err=%v", result, err)
			}
			if test.wantVersion == "" {
				if len(result.Install) != 0 {
					t.Fatalf("excluded range was updated: %#v", result.Install)
				}
			} else if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != test.wantVersion || result.Install[0].Repository != "gentoo" {
				t.Fatalf("repository exclusion removed an eligible version: %#v", result.Install)
			}
		})
	}
}

func TestResolveExcludePreventsBlockerRemoval(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/replacement", "1", "0", "0", false, nil)
	parent.Rdepend = "!app-misc/obsolete"
	pkg(g, "app-misc/obsolete", "1", "0", "0", true, nil)
	cfg := DefaultResolveConfig()
	cfg.Exclude = []string{"app-misc/obsolete"}
	result, err := Resolve(g, []string{"app-misc/replacement"}, cfg)
	if err == nil && result.Verified {
		t.Fatal("blocker removal bypassed exclusion")
	}
	if result != nil && len(result.Uninstall) != 0 {
		t.Fatal("failed plan still removes the excluded package")
	}
}
