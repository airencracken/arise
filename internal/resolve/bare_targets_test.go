package resolve

import (
	"slices"
	"strings"
	"testing"
)

func TestResolveBareNameCategoryPreference(t *testing.T) {
	for _, test := range []struct {
		name     string
		packages []string
		want     string
	}{
		{"application and accounts", []string{"acct-group/tool", "acct-user/tool", "www-apps/tool"}, "www-apps/tool"},
		{"application and virtual", []string{"virtual/tool", "www-apps/tool"}, "www-apps/tool"},
		{"application accounts and virtual", []string{"acct-group/tool", "acct-user/tool", "virtual/tool", "www-apps/tool"}, "www-apps/tool"},
		{"unique account", []string{"acct-user/tool"}, "acct-user/tool"},
		{"unique virtual", []string{"virtual/tool"}, "virtual/tool"},
		{"accounts only", []string{"acct-group/tool", "acct-user/tool"}, ""},
		{"account and virtual", []string{"acct-user/tool", "virtual/tool"}, ""},
		{"multiple applications", []string{"app-misc/tool", "www-apps/tool"}, ""},
		{"multiple applications with accounts", []string{"acct-group/tool", "acct-user/tool", "app-misc/tool", "www-apps/tool"}, ""},
		{"category prefix is not special", []string{"acct-user-extra/tool", "www-apps/tool"}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := makeGraph()
			for _, cp := range test.packages {
				pkg(g, cp, "1", "0", "0", false, nil)
			}
			result, err := Resolve(g, []string{"tool"}, DefaultResolveConfig())
			if test.want == "" {
				want := "[" + strings.Join(test.packages, ", ") + "]"
				if err == nil || !strings.Contains(err.Error(), "ambiguous package name") || !strings.Contains(err.Error(), want) {
					t.Fatalf("ambiguity error = %v, want all candidates %s", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !result.Verified || len(result.Install) != 1 || result.Install[0].Atom.CP() != test.want {
				t.Fatalf("plan = %v (verified %v), want only %s", collectCPV(result.Install), result.Verified, test.want)
			}
		})
	}
}

func TestResolveBareApplicationIncludesAccountDependencies(t *testing.T) {
	g := makeGraph()
	for _, cp := range []string{"acct-group/imvault", "acct-user/imvault", "www-apps/imvault"} {
		version := pkg(g, cp, "1", "0", "0", false, nil)
		version.EAPI, version.DependencyMetadataKnown = "8", true
		if cp == "www-apps/imvault" {
			version.Rdepend = "acct-user/imvault"
		} else if cp == "acct-user/imvault" {
			version.Rdepend = "acct-group/imvault"
		}
	}
	for _, test := range []struct {
		target string
		want   []string
	}{
		{"imvault", []string{"acct-group/imvault-1", "acct-user/imvault-1", "www-apps/imvault-1"}},
		{"acct-user/imvault", []string{"acct-group/imvault-1", "acct-user/imvault-1"}},
		{"acct-group/imvault", []string{"acct-group/imvault-1"}},
	} {
		t.Run(test.target, func(t *testing.T) {
			result, err := Resolve(g, []string{test.target}, DefaultResolveConfig())
			if err != nil {
				t.Fatal(err)
			}
			got := collectCPV(result.Install)
			slices.Sort(got)
			if !result.Verified || !slices.Equal(got, test.want) {
				t.Fatalf("plan = %v (verified %v), want %v", got, result.Verified, test.want)
			}
		})
	}
}

func TestResolveMissingTargetSuggestsBinaryAlternative(t *testing.T) {
	for _, test := range []struct {
		target string
		cp     string
	}{
		{"signal-desktop", "net-im/signal-desktop-bin"},
		{"net-im/signal-desktop", "net-im/signal-desktop-bin"},
		{"=net-im/signal-desktop-8.28.0", "net-im/signal-desktop-bin"},
		{"firefox", "www-client/firefox-bin"},
		{"firefox-bin", "www-client/firefox"},
	} {
		t.Run(test.target, func(t *testing.T) {
			g := makeGraph()
			pkg(g, test.cp, "8.28.0", "0", "0", false, nil)
			result, err := Resolve(g, []string{test.target}, DefaultResolveConfig())
			if err == nil || !strings.Contains(err.Error(), "maybe you meant: "+test.cp) {
				t.Fatalf("missing target diagnostic = %v, want suggestion %s", err, test.cp)
			}
			if strings.Contains(err.Error(), "arise sync") || strings.Contains(err.Error(), "arise index") {
				t.Fatalf("useful suggestion was obscured by index refresh advice: %v", err)
			}
			if result != nil && actionForCP(result.Install, test.cp) {
				t.Fatalf("suggested package was silently selected: %v", collectCPV(result.Install))
			}
		})
	}
}

func TestBarePackageSuggestionsRankBinaryAlternativeAndBoundResults(t *testing.T) {
	g := makeGraph()
	for _, cp := range []string{
		"net-im/signal-desktop-bin", "net-im/signal-desktops",
		"app-misc/signal-desktops", "app-misc/signal-desktop-bin",
		"net-im/signal-cli-bin", "dev-libs/openssl",
	} {
		pkg(g, cp, "1", "0", "0", false, nil)
	}
	r := &resolver{graph: g}
	want := []string{"app-misc/signal-desktop-bin", "net-im/signal-desktop-bin", "app-misc/signal-desktops"}
	for range 10 {
		if got := r.packageSuggestions("signal-desktop", 3); !slices.Equal(got, want) {
			t.Fatalf("bare suggestions = %v, want %v", got, want)
		}
	}
}

func TestResolveMissingBareNameSuggestsTypoCorrection(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.1", "0", "0", false, nil)
	pkg(g, "dev-libs/openssl", "3", "0", "0", false, nil)
	_, err := Resolve(g, []string{"vimn"}, DefaultResolveConfig())
	if err == nil || !strings.Contains(err.Error(), "maybe you meant: app-editors/vim") || strings.Contains(err.Error(), "openssl") {
		t.Fatalf("bare typo diagnostic = %v", err)
	}
}

func TestResolveMissingBareNameWithoutSuggestionKeepsRefreshHint(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/openssl", "3", "0", "0", false, nil)
	_, err := Resolve(g, []string{"signal-desktop"}, DefaultResolveConfig())
	if err == nil || !strings.Contains(err.Error(), "arise sync or arise index") || strings.Contains(err.Error(), "maybe you meant") {
		t.Fatalf("missing bare target diagnostic = %v", err)
	}
}

func TestResolveExactBareNameWinsOverBinaryAlternative(t *testing.T) {
	g := makeGraph()
	pkg(g, "net-im/signal-desktop", "8.28.0", "0", "0", false, nil)
	pkg(g, "net-im/signal-desktop-bin", "8.28.0", "0", "0", false, nil)
	result, err := Resolve(g, []string{"signal-desktop"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || !slices.Equal(collectCPV(result.Install), []string{"net-im/signal-desktop-8.28.0"}) {
		t.Fatalf("exact target plan = %v", collectCPV(result.Install))
	}
}

func TestResolveMissingBareNameKeepGoingRetainsSuggestion(t *testing.T) {
	g := makeGraph()
	pkg(g, "net-im/signal-desktop-bin", "8.28.0", "0", "0", false, nil)
	pkg(g, "app-editors/vim", "9.1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true
	result, err := Resolve(g, []string{"signal-desktop", "vim"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || !strings.Contains(result.Conflicts[0], "maybe you meant: net-im/signal-desktop-bin") {
		t.Fatalf("partial plan conflicts = %v", result.Conflicts)
	}
	if result.Verified || !slices.Equal(collectCPV(result.Install), []string{"app-editors/vim-9.1"}) {
		t.Fatalf("partial plan = %v, verified %v", collectCPV(result.Install), result.Verified)
	}
}
