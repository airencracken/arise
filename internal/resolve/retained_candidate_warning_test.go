package resolve

import (
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestWorldRetainsNewerInstalledVersionWithOlderRepositoryCandidate(t *testing.T) {
	graph := makeGraph()
	graph.AddVersionFromRepository("sys-apps/arise", "0.0.49", "0", "0", true, nil, "", "arise-overlay")
	graph.AddVersionFromRepository("sys-apps/arise", "0.0.47", "0", "0", false, nil, "~amd64", "arise-overlay")
	config := DefaultResolveConfig()
	config.WorldSet = &WorldSet{Entries: []string{"sys-apps/arise"}}
	config.Update, config.Deep = true, true
	result, err := Resolve(graph, []string{"@world"}, config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Install) != 0 || len(result.Uninstall) != 0 {
		t.Fatalf("newer installed version was not retained: %#v", result)
	}
	warnings := strings.Join(result.Warnings, "\n")
	if strings.Contains(warnings, "no installable repository candidate") || strings.Contains(warnings, "arise deselect") {
		t.Fatalf("older available candidate was mistaken for an obsolete world selection: %s", warnings)
	}
}

func TestRetainedWorldWarningDistinguishesPolicyFromMissingCandidate(t *testing.T) {
	for _, test := range []struct {
		name, selection, candidateVersion, slot, repository, keywords string
		mask, exclude                                                 []string
		want, absent                                                  string
	}{
		{name: "empty repository", selection: "sys-apps/arise", want: "no installable repository candidate", absent: "blocked by current policy"},
		{name: "masked older version", selection: "sys-apps/arise", candidateVersion: "0.0.47", slot: "0", repository: "arise-overlay", keywords: "amd64", mask: []string{"=sys-apps/arise-0.0.47"}, want: "blocked by current policy (package masking)", absent: "arise deselect"},
		{name: "unaccepted keyword", selection: "sys-apps/arise", candidateVersion: "0.0.47", slot: "0", repository: "arise-overlay", keywords: "~amd64", want: "blocked by current policy (keyword acceptance)", absent: "arise deselect"},
		{name: "excluded older version", selection: "sys-apps/arise", candidateVersion: "0.0.47", slot: "0", repository: "arise-overlay", keywords: "amd64", exclude: []string{"=sys-apps/arise-0.0.47"}, want: "blocked by current policy (--exclude)", absent: "arise deselect"},
		{name: "pinned missing version", selection: "=sys-apps/arise-0.0.49", candidateVersion: "0.0.47", slot: "0", repository: "arise-overlay", keywords: "amd64", want: "no installable repository candidate", absent: "blocked by current policy"},
		{name: "different slot", selection: "sys-apps/arise:0", candidateVersion: "0.0.47", slot: "1", repository: "arise-overlay", keywords: "amd64", want: "no installable repository candidate", absent: "blocked by current policy"},
		{name: "different repository", selection: "sys-apps/arise::arise-overlay", candidateVersion: "0.0.47", slot: "0", repository: "other", keywords: "amd64", want: "no installable repository candidate", absent: "blocked by current policy"},
		{name: "different USE", selection: "sys-apps/arise[feature]", candidateVersion: "0.0.47", slot: "0", repository: "arise-overlay", keywords: "amd64", want: "no installable repository candidate", absent: "blocked by current policy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := makeGraph()
			graph.AddVersionFromRepository("sys-apps/arise", "0.0.49", "0", "0", true, map[string]bool{"feature": true}, "", "arise-overlay")
			if test.candidateVersion != "" {
				graph.AddVersionFromRepository("sys-apps/arise", test.candidateVersion, test.slot, test.slot, false, map[string]bool{"feature": false}, test.keywords, test.repository)
			}
			config := DefaultResolveConfig()
			config.WorldSet = &WorldSet{Entries: []string{test.selection}}
			config.Update, config.Deep = true, true
			config.Exclude = test.exclude
			config.PortageConfig = &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}, ACCEPT_KEYWORDS: []string{"amd64"}, PackageMask: test.mask}
			result, err := Resolve(graph, []string{"@world"}, config)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Verified || len(result.Install) != 0 || len(result.Uninstall) != 0 {
				t.Fatalf("retention changed the package plan: %#v", result)
			}
			warnings := strings.Join(result.Warnings, "\n")
			if !strings.Contains(warnings, "world selection "+test.selection) {
				t.Fatalf("warning lost the saved selection constraint: %s", warnings)
			}
			if !strings.Contains(warnings, test.want) || strings.Contains(warnings, test.absent) {
				t.Fatalf("warning does not describe the available evidence: %s", warnings)
			}
			if test.want == "no installable repository candidate" && !strings.Contains(warnings, "run arise index after external repository updates") {
				t.Fatalf("missing candidate warning omits external-sync recovery: %s", warnings)
			}
		})
	}
}

func TestRetainedDependencyWithOlderCandidateDoesNotClaimItIsUnavailable(t *testing.T) {
	graph := makeGraph()
	parent := pkg(graph, "app-misc/parent", "1", "0", "0", true, nil)
	parent.Available = true
	parent.InstalledRdepend = "sys-apps/arise"
	graph.AddVersionFromRepository("sys-apps/arise", "0.0.49", "0", "0", true, nil, "", "arise-overlay")
	graph.AddVersionFromRepository("sys-apps/arise", "0.0.47", "0", "0", false, nil, "~amd64", "arise-overlay")
	config := DefaultResolveConfig()
	config.WorldSet = &WorldSet{Entries: []string{"app-misc/parent"}}
	config.Update, config.Deep = true, true
	config.DynamicDeps = false
	result, err := Resolve(graph, []string{"@world"}, config)
	if err != nil || !result.Verified || len(result.Install) != 0 || len(result.Uninstall) != 0 {
		t.Fatalf("retained dependency changed the package plan: result=%#v err=%v", result, err)
	}
	if warnings := strings.Join(result.Warnings, "\n"); strings.Contains(warnings, "no matching repository candidate") {
		t.Fatalf("available dependency candidate was treated as missing: %s", warnings)
	}
}
