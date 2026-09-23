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
