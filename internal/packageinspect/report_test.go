package packageinspect

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/gentooling"
)

func TestBuildUnifiedReport(t *testing.T) {
	snapshot, repository := inspectFixture(t)
	report, err := Build(context.Background(), snapshot, Options{
		Query: "sys-fs/zfs-kmod", Repositories: []gentooling.Repository{repository}, TargetKernel: "6.13",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != Schema || report.Consistency != "stabilized-lockless" {
		t.Fatalf("report identity = %+v", report)
	}
	if len(report.Installed) != 1 || len(report.Candidates) != 1 {
		t.Fatalf("package matches = installed %d candidates %d", len(report.Installed), len(report.Candidates))
	}
	candidate := report.Candidates[0]
	if candidate.Visibility.Status != gentooling.VisibilityKeywordMasked || candidate.Visibility.Visible {
		t.Fatalf("visibility = %+v", candidate.Visibility)
	}
	decision, found := candidate.Use.Decision("modules")
	if !found || !decision.Enabled {
		t.Fatalf("USE decision = %+v, found %v", decision, found)
	}
	if len(candidate.KernelRequirements.Requirements) != 1 ||
		candidate.KernelRequirements.Requirements[0].Symbol != "MODULES" {
		t.Fatalf("kernel requirements = %+v", candidate.KernelRequirements)
	}
	if !reflect.DeepEqual(report.RequiredBy, []string{"app-admin/consumer-1"}) {
		t.Fatalf("required by = %v", report.RequiredBy)
	}
	if len(report.Modules) != 1 || !report.Modules[0].NeedsRebuild ||
		report.Modules[0].Rebuild != gentooling.KernelModuleTargetMissing {
		t.Fatalf("module state = %+v", report.Modules)
	}
}

func TestBuildReturnsTypedNotFoundWithReport(t *testing.T) {
	snapshot, _ := inspectFixture(t)
	report, err := Build(context.Background(), snapshot, Options{Query: "dev-libs/missing"})
	if !errors.Is(err, gentooling.ErrCandidateNotFound) {
		t.Fatalf("error = %v", err)
	}
	if report.Schema != Schema || report.Installed == nil || report.Candidates == nil {
		t.Fatalf("partial report contract = %+v", report)
	}
}

func TestBuildRecordsMalformedDependencyWithoutDiscardingPackage(t *testing.T) {
	snapshot, repository := inspectFixture(t)
	snapshot.Candidates.Candidates[0].Dependencies.RDepend = "|| broken"
	report, err := Build(context.Background(), snapshot, Options{
		Query: "sys-fs/zfs-kmod", Repositories: []gentooling.Repository{repository},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range report.Diagnostics {
		if issue.Code == "dependency_parse" && issue.Package == "sys-fs/zfs-kmod-2.3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func TestReportJSONHasStableNonNullCollections(t *testing.T) {
	snapshot, repository := inspectFixture(t)
	report, err := Build(context.Background(), snapshot, Options{
		Query: "=sys-fs/zfs-kmod-2.3", Repositories: []gentooling.Repository{repository},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"installed", "candidates", "required_by", "kernel_modules", "diagnostics"} {
		if document[field] == nil {
			t.Fatalf("%s is null in %s", field, data)
		}
	}
	installed := document["installed"].([]any)[0].(map[string]any)
	pkg := installed["package"].(map[string]any)
	if pkg["category"] != "sys-fs" || pkg["Category"] != nil {
		t.Fatalf("package JSON keys = %#v", pkg)
	}
	dependencies := installed["dependencies"].(map[string]any)
	if _, found := dependencies["r_depend"]; !found {
		t.Fatalf("dependency JSON keys = %#v", dependencies)
	}
}

func TestBuildSummarizesUnsupportedReverseDependencyAtoms(t *testing.T) {
	snapshot, repository := inspectFixture(t)
	snapshot.Installed.Packages[0].Dependencies.RDepend = "${RUNTIME_DEPS}"
	report, err := Build(context.Background(), snapshot, Options{
		Query: "sys-fs/zfs-kmod", Repositories: []gentooling.Repository{repository},
	})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, issue := range report.Diagnostics {
		if issue.Code == "reverse_dependency_incomplete" {
			count++
		}
		if issue.Code == "dependency_atom" {
			t.Fatalf("per-atom diagnostic leaked: %+v", issue)
		}
	}
	if count != 1 {
		t.Fatalf("summary diagnostics = %+v", report.Diagnostics)
	}
}

func TestBuildRejectsAdversarialQueries(t *testing.T) {
	snapshot, _ := inspectFixture(t)
	for _, query := range []string{"", "../sys-fs/zfs-kmod", "sys-fs/zfs-kmod\nother", "sys-fs/zfs-kmod[bad flag]"} {
		t.Run(query, func(t *testing.T) {
			if _, err := Build(context.Background(), snapshot, Options{Query: query}); err == nil {
				t.Fatalf("Build(%q) succeeded", query)
			}
		})
	}
}

func FuzzBuildQuery(f *testing.F) {
	f.Add("sys-fs/zfs-kmod")
	f.Add("=sys-fs/zfs-kmod-2.3")
	f.Add("../bad")
	f.Fuzz(func(t *testing.T, query string) {
		snapshot := gentooling.SystemSnapshot{
			Config:      gentooling.EffectiveConfig{Variables: map[string]string{"ARCH": "amd64"}},
			Consistency: gentooling.StabilizedLockless,
		}
		_, _ = Build(context.Background(), snapshot, Options{Query: query})
	})
}

func inspectFixture(t *testing.T) (gentooling.SystemSnapshot, gentooling.Repository) {
	t.Helper()
	root := t.TempDir()
	ebuild := filepath.Join(root, "sys-fs", "zfs-kmod", "zfs-kmod-2.3.ebuild")
	if err := os.MkdirAll(filepath.Dir(ebuild), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ebuild, []byte("CONFIG_CHECK=\"MODULES\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := gentooling.PackageID{Category: "sys-fs", Name: "zfs-kmod", Version: "2.3", Slot: "0", Repository: "gentoo"}
	installed := gentooling.InstalledPackage{
		ID: id, EAPI: "8", EnabledUse: []string{"modules"},
		DeclaredUse:  []gentooling.UseDeclaration{{Name: "modules"}},
		Inherited:    []string{"linux-mod-r1"},
		Dependencies: gentooling.DependencyMetadata{RDepend: "sys-kernel/gentoo-kernel"},
		Contents:     "obj /lib/modules/6.12/extra/zfs.ko.zst hash 1\n",
	}
	consumer := gentooling.InstalledPackage{
		ID:   gentooling.PackageID{Category: "app-admin", Name: "consumer", Version: "1"},
		EAPI: "8", Dependencies: gentooling.DependencyMetadata{RDepend: "sys-fs/zfs-kmod"},
	}
	candidate := gentooling.RepositoryCandidate{
		ID: id, EAPI: "8", Keywords: []string{"~amd64"},
		DeclaredUse:  []gentooling.UseDeclaration{{Name: "modules", Default: gentooling.UseDefaultEnabled}},
		Dependencies: gentooling.DependencyMetadata{RDepend: "sys-kernel/gentoo-kernel"},
	}
	repository := gentooling.Repository{Name: "gentoo", Location: root}
	return gentooling.SystemSnapshot{
		Installed:    gentooling.InstalledInventory{Packages: []gentooling.InstalledPackage{consumer, installed}},
		Config:       gentooling.EffectiveConfig{Variables: map[string]string{"ARCH": "amd64"}},
		Repositories: []gentooling.Repository{repository},
		Candidates:   gentooling.CandidateInventory{Candidates: []gentooling.RepositoryCandidate{candidate}},
		Consistency:  gentooling.StabilizedLockless,
	}, repository
}
