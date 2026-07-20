package resolve

import "testing"

type eapiContract struct {
	name                 string
	slotDependencies     bool
	iuseDefaults         bool
	useDependencies      bool
	strongBlockers       bool
	useDefaults          bool
	requiredUse          bool
	slotOperators        bool
	bdepend              bool
	idepend              bool
	dependDomain         DependencyDomain
	inactiveAnyOfIsError bool
}

// eapiContracts is the single extension point for a newly supported EAPI.
// Append its capability row here; all contract tests below acquire the gate.
var eapiContracts = []eapiContract{
	{name: "0", dependDomain: DomainBROOT},
	{name: "1", slotDependencies: true, iuseDefaults: true, dependDomain: DomainBROOT},
	{name: "2", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, dependDomain: DomainBROOT},
	{name: "3", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, dependDomain: DomainBROOT},
	{name: "4", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, useDefaults: true, requiredUse: true, dependDomain: DomainBROOT},
	{name: "5", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, useDefaults: true, requiredUse: true, slotOperators: true, dependDomain: DomainBROOT},
	{name: "6", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, useDefaults: true, requiredUse: true, slotOperators: true, dependDomain: DomainBROOT},
	{name: "7", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, useDefaults: true, requiredUse: true, slotOperators: true, bdepend: true, dependDomain: DomainSYSROOT, inactiveAnyOfIsError: true},
	{name: "8", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, useDefaults: true, requiredUse: true, slotOperators: true, bdepend: true, idepend: true, dependDomain: DomainSYSROOT, inactiveAnyOfIsError: true},
	{name: "9", slotDependencies: true, iuseDefaults: true, useDependencies: true, strongBlockers: true, useDefaults: true, requiredUse: true, slotOperators: true, bdepend: true, idepend: true, dependDomain: DomainSYSROOT, inactiveAnyOfIsError: true},
}

// TestEAPIDependencySyntaxContract is deliberately organized by EAPI rather
// than parser feature. It is the executable compatibility table for package
// dependency syntax accepted by Portage through the currently defined EAPIs.
func TestEAPIDependencySyntaxContract(t *testing.T) {
	features := []struct {
		name       string
		dependency string
		enabled    func(eapiContract) bool
	}{
		{name: "plain atom", dependency: "app-misc/provider", enabled: func(eapiContract) bool { return true }},
		{name: "slot dependency", dependency: "app-misc/provider:0", enabled: func(c eapiContract) bool { return c.slotDependencies }},
		{name: "USE dependency", dependency: "app-misc/provider[feature]", enabled: func(c eapiContract) bool { return c.useDependencies }},
		{name: "strong blocker", dependency: "!!app-misc/provider", enabled: func(c eapiContract) bool { return c.strongBlockers }},
		{name: "USE dependency default", dependency: "app-misc/provider[feature(+)]", enabled: func(c eapiContract) bool { return c.useDefaults }},
		{name: "slot operator", dependency: "app-misc/provider:=", enabled: func(c eapiContract) bool { return c.slotOperators }},
		{name: "repository qualifier", dependency: "app-misc/provider::gentoo", enabled: func(eapiContract) bool { return false }},
	}

	for _, contract := range eapiContracts {
		t.Run("EAPI_"+contract.name, func(t *testing.T) {
			graph := makeGraph()
			node := graph.AddPackage("app-misc/consumer")
			resolver := &resolver{graph: graph, baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
			for _, feature := range features {
				t.Run(feature.name, func(t *testing.T) {
					version := &VersionInfo{
						Package: node, Version: mustParse("app-misc/consumer-1").Version,
						Available: true, Rdepend: feature.dependency, EAPI: contract.name,
					}
					_, err := resolver.dependenciesForVersion(node, version)
					wantValid := feature.enabled(contract)
					if wantValid && err != nil {
						t.Fatalf("valid dependency rejected: %v", err)
					}
					if !wantValid && err == nil {
						t.Fatalf("dependency unexpectedly valid in EAPI %s", contract.name)
					}
				})
			}
		})
	}
}

func TestEAPIMetadataContract(t *testing.T) {
	for _, contract := range eapiContracts {
		t.Run("EAPI_"+contract.name, func(t *testing.T) {
			for _, prefix := range []string{"+", "-"} {
				if err := validateVersionMetadataEAPI(&VersionInfo{EAPI: contract.name, IUse: prefix + "feature"}); (err == nil) != contract.iuseDefaults {
					t.Fatalf("IUSE %q default error=%v, enabled=%v", prefix, err, contract.iuseDefaults)
				}
			}
			if err := validateVersionMetadataEAPI(&VersionInfo{EAPI: contract.name, RequiredUse: "feature"}); (err == nil) != contract.requiredUse {
				t.Fatalf("REQUIRED_USE error=%v, enabled=%v", err, contract.requiredUse)
			}
		})
	}
	if err := validateVersionMetadataEAPI(&VersionInfo{EAPI: "9999", IUse: "+feature", RequiredUse: "feature"}); err != nil {
		t.Fatalf("future EAPI lost forward compatibility: %v", err)
	}
}

func TestEAPIInactiveAnyOfContract(t *testing.T) {
	for _, contract := range eapiContracts {
		t.Run("EAPI_"+contract.name, func(t *testing.T) {
			graph := makeGraph()
			consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, map[string]bool{"gui": false})
			consumer.EAPI = contract.name
			consumer.Rdepend = "|| ( gui? ( app-misc/provider ) )"
			result, err := Resolve(graph, []string{"app-misc/consumer"}, DefaultResolveConfig())
			gotError := err != nil || len(result.Conflicts) != 0
			if gotError != contract.inactiveAnyOfIsError {
				t.Fatalf("inactive any-of error=%v conflicts=%v, wantError=%v", err, result.Conflicts, contract.inactiveAnyOfIsError)
			}
		})
	}
}

func TestEAPIDependencyClassContract(t *testing.T) {
	for _, contract := range eapiContracts {
		t.Run("EAPI_"+contract.name, func(t *testing.T) {
			graph := makeGraph()
			node := graph.AddPackage("app-misc/consumer")
			version := &VersionInfo{
				Package: node, Version: mustParse("app-misc/consumer-1").Version, Available: true,
				Bdepend: "dev-build/tool", Idepend: "app-admin/tool", EAPI: contract.name,
			}
			resolver := &resolver{graph: graph, baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
			edges, err := resolver.dependenciesForVersion(node, version)
			if err != nil {
				t.Fatal(err)
			}
			gotBuild, gotInstall := false, false
			for _, edge := range edges {
				gotBuild = gotBuild || edge.Type == DepTypeBuild
				gotInstall = gotInstall || edge.Type == DepTypeInstall
			}
			if gotBuild != contract.bdepend || gotInstall != contract.idepend {
				t.Fatalf("BDEPEND active=%v IDEPEND active=%v", gotBuild, gotInstall)
			}
			if got := dependencyDomainForEAPI(DepTypeDepend, contract.name); got != contract.dependDomain {
				t.Fatalf("DEPEND domain=%s, want %s", got, contract.dependDomain)
			}
		})
	}
}
