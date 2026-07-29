package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/executor"
	"github.com/airencracken/arise/internal/planadapter"
	"github.com/airencracken/arise/internal/plancompare"
	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/vdb"
)

func TestResolverPlanClassificationExecutionAndObservedState(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	root := filepath.Join(base, "root")
	vdbDir := filepath.Join(root, "var", "db", "pkg")
	for _, directory := range []string{
		filepath.Join(repo, "eclass"),
		filepath.Join(repo, "dev-libs", "provider"),
		filepath.Join(repo, "app-misc", "consumer"),
		root, vdbDir, filepath.Join(base, "work"), filepath.Join(base, "distfiles"),
		filepath.Join(base, "logs"), filepath.Join(base, "journals"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeEbuild := func(relative, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, relative), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const body = `
src_unpack() { mkdir -p "${S}"; }
src_install() { :; }
`
	writeEbuild("dev-libs/provider/provider-1.ebuild", "EAPI=8\nSLOT=\"0/1\"\n"+body)
	writeEbuild("dev-libs/provider/provider-2.ebuild", "EAPI=8\nSLOT=\"0/2\"\n"+body)
	writeEbuild("app-misc/consumer/consumer-1.ebuild", "EAPI=8\nSLOT=\"0\"\nRDEPEND=\"dev-libs/provider:=\"\n"+body)

	rebuildConfig := rebuild.RebuildConfig{
		RepoDir: repo, Repository: "test",
		Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
		RootDir:      root, VdbDir: vdbDir, WorkDirBase: filepath.Join(base, "work"),
		DistfilesDir: filepath.Join(base, "distfiles"), PhaseProtocol: true,
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
	}
	for _, cpv := range []string{"dev-libs/provider-1", "app-misc/consumer-1"} {
		if err := rebuild.RebuildPackage(context.Background(), cpv, &rebuildConfig); err != nil {
			t.Fatalf("prepare disposable state with %s: %v", cpv, err)
		}
	}
	rebuildConfig.AllowLiveUpgrade = true
	if err := rebuild.RebuildPackage(context.Background(), "dev-libs/provider-2", &rebuildConfig); err != nil {
		t.Fatalf("prepare provider subslot transition: %v", err)
	}
	rebuildConfig.AllowLiveUpgrade = false

	graph := resolve.NewDepGraph()
	provider := graph.AddVersionFromRepository("dev-libs/provider", "2", "0", "2", true, nil, "amd64", "test")
	provider.InstalledEAPI, provider.EAPI, provider.DependencyMetadataKnown = "8", "8", true
	consumer := graph.AddVersionFromRepository("app-misc/consumer", "1", "0", "0", true, nil, "amd64", "test")
	consumer.InstalledRdepend = "dev-libs/provider:0/1="
	consumer.InstalledEAPI, consumer.DependencyMetadataKnown = "8", true
	consumer = graph.AddVersionFromRepository("app-misc/consumer", "1", "0", "0", false, nil, "amd64", "test")
	consumer.Rdepend, consumer.EAPI, consumer.RepositoryPath = "dev-libs/provider:=", "8", repo
	consumer.DependencyMetadataKnown = true

	resolveConfig := resolve.DefaultResolveConfig()
	resolveConfig.CompleteGraph = true
	resolved, err := resolve.Resolve(graph, []string{"app-misc/consumer"}, resolveConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Verified || len(resolved.Install) != 1 ||
		resolved.Install[0].Action != "reinstall" ||
		resolved.Install[0].Atom.CP() != "app-misc/consumer" {
		t.Fatalf("resolver repair plan = %#v", resolved)
	}
	fixture, arisePlan, err := planadapter.Freeze(graph, resolved, planadapter.Options{
		Targets: []string{"app-misc/consumer"}, DomainsAliasToRoot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ariseValidation := planvalidate.ValidatePlanImpact(fixture, arisePlan)
	if !ariseValidation.Valid {
		t.Fatalf("resolver-produced plan failed independent validation: %#v", ariseValidation)
	}
	portagePlan := planvalidate.Plan{Schema: planvalidate.SchemaVersion}
	portageValidation := planvalidate.ValidatePlanImpact(fixture, portagePlan)
	if !portageValidation.Valid {
		t.Fatalf("retained baseline failed impact validation: %#v", portageValidation)
	}
	ariseState := planvalidate.ApplyPlan(fixture.Installed, arisePlan).State
	portageState := planvalidate.ApplyPlan(fixture.Installed, portagePlan).State
	classified, err := plancompare.ClassifyFinalStates(
		plancompare.AssessmentFromValidation(ariseValidation, ariseState),
		plancompare.AssessmentFromValidation(portageValidation, portageState),
		plancompare.ClassificationPolicyForRequest(fixture.Request),
		[]plancompare.Difference{{Identity: "app-misc/consumer:0", Kind: "only-arise"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if classified.Class != plancompare.ClassValidDivergence || classified.Equivalent ||
		len(classified.ActionDiagnostics) != 1 {
		t.Fatalf("classified repair = %#v", classified)
	}
	predicted, err := planvalidate.PredictCommittedState(ariseState)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(context.Background(), resolved, executor.Config{
		Rebuild: rebuildConfig, Jobs: 1, ResumePath: filepath.Join(base, "resume.json"),
	}); err != nil {
		t.Fatalf("execute exact resolver plan: %v", err)
	}
	observedPackages, err := vdb.ScanResolverState(vdbDir)
	if err != nil {
		t.Fatal(err)
	}
	observed := planadapter.StateFromVDB(observedPackages)
	if result := planvalidate.ValidateCommittedState(predicted, observed); !result.Valid {
		t.Fatalf("observed execution diverged from prediction: %#v", result)
	}
}
