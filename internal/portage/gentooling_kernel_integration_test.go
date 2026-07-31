package portage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/gentooling"
)

func TestGentoolingKernelRequirementConsumerContract(t *testing.T) {
	repository := t.TempDir()
	ebuild := filepath.Join(repository, "sys-fs", "module", "module-1.ebuild")
	if err := os.MkdirAll(filepath.Dir(ebuild), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ebuild, []byte(`CONFIG_CHECK="MODULES ~MODVERSIONS"
pkg_setup() {
	check_extra_config
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate := gentooling.RepositoryCandidate{
		ID: gentooling.PackageID{Category: "sys-fs", Name: "module", Version: "1", Repository: "test"},
	}
	result, err := gentooling.EvaluateKernelRequirements(context.Background(), candidate,
		[]gentooling.Repository{{Name: "test", Location: repository}},
		gentooling.KernelRequirementContext{Phase: "pkg_setup", KernelRelease: "7.1.5", EffectiveUSE: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 2 || result.Requirements[0].Symbol != "MODULES" ||
		result.Requirements[0].Applicability != gentooling.Applicable || !result.Complete {
		t.Fatalf("kernel requirements = %+v", result)
	}
}

func TestGentoolingProspectiveSnapshotConsumerContract(t *testing.T) {
	snapshot := gentooling.SystemSnapshot{
		Config: gentooling.EffectiveConfig{Variables: map[string]string{"ARCH": "amd64"}},
		Candidates: gentooling.CandidateInventory{Candidates: []gentooling.RepositoryCandidate{{
			ID:          gentooling.PackageID{Category: "sys-kernel", Name: "example", Version: "1", Repository: "gentoo"},
			Keywords:    []string{"amd64"},
			DeclaredUse: []gentooling.UseDeclaration{{Name: "modules", Default: gentooling.UseDefaultEnabled}},
		}}},
	}
	result, err := snapshot.EvaluateCandidate(context.Background(), gentooling.PackageID{
		Category: "sys-kernel", Name: "example", Version: "1", Repository: "gentoo",
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, found := result.Use.Decision("modules")
	if !result.Visibility.Visible || !found || !decision.Enabled {
		t.Fatalf("prospective evaluation = %+v", result)
	}
}
