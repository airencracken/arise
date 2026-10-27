package executor

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

func blockerRemovalFixture(t *testing.T, blocker string) (*resolve.ResolveResult, Config, string) {
	t.Helper()
	base := t.TempDir()
	root, repo := filepath.Join(base, "root"), filepath.Join(base, "repo")
	cfg := Config{ResumePath: filepath.Join(base, "resume"), Rebuild: rebuild.RebuildConfig{
		RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		PhaseProtocol: true, Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}}
	oldPath := filepath.Join(cfg.Rebuild.VdbDir, "perl-core/Test-Harness-3.500.0")
	for _, dir := range []string{oldPath, filepath.Join(root, "usr/share/harness"), filepath.Join(repo, "eclass")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	payload := filepath.Join(root, "usr/share/harness/current")
	if err := os.WriteFile(payload, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	contents := fmt.Sprintf("obj /usr/share/harness/current %x %d\n", md5.Sum([]byte("old\n")), info.ModTime().Unix())
	for name, data := range map[string]string{
		"CONTENTS": contents, "SLOT": "0\n", "repository": "test\n", "EAPI": "8\n",
		"Test-Harness-3.500.0.ebuild": "EAPI=8\nSLOT=0\n",
	} {
		if err := os.WriteFile(filepath.Join(oldPath, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// The saved environment is authoritative even when current eclasses or
	// the stored ebuild no longer define these hooks.
	environment := "EAPI=8\n" +
		"pkg_prerm ()\n{\n echo prerm >> \"${ROOT}/hooks\";\n}\n" +
		"pkg_postrm ()\n{\n echo postrm >> \"${ROOT}/hooks\";\n}\n"
	compress := exec.Command("bzip2", "-c")
	compress.Stdin = strings.NewReader(environment)
	compressed, err := compress.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "environment.bz2"), compressed, 0644); err != nil {
		t.Fatal(err)
	}
	graph := resolve.NewDepGraph()
	old := graph.AddVersionFromRepository("perl-core/Test-Harness", "3.500.0", "0", "0", true, nil, "amd64", "test")
	old.InstalledEAPI, old.DependencyMetadataKnown = "8", true
	parent := graph.AddVersionFromRepository("virtual/perl-Test-Harness", "3.520.0", "0", "0", false, nil, "amd64", "test")
	parent.Available, parent.DependencyMetadataKnown, parent.EAPI = true, true, "8"
	parent.Rdepend = blocker + "<perl-core/Test-Harness-3.520.0"
	plan, err := resolve.Resolve(graph, []string{"virtual/perl-Test-Harness"}, resolve.DefaultResolveConfig())
	if err != nil || plan == nil || !plan.Verified || len(plan.Install) != 1 || len(plan.Uninstall) != 1 {
		t.Fatalf("mixed plan: %#v, %v", plan, err)
	}
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(filepath.Join(image, "usr/share/harness"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "usr/share/harness/current"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(base, "harness.gpkg.tar")
	if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{
		Path: binary, Basename: "perl-Test-Harness-3.520.0", ImageRoot: image,
		Metadata: map[string][]byte{"CATEGORY": []byte("virtual"), "PF": []byte("perl-Test-Harness-3.520.0"), "SLOT": []byte("0/0"), "EAPI": []byte("8")},
	}); err != nil {
		t.Fatal(err)
	}
	plan.Install[0].RepositoryPath, plan.Install[0].MergeType, plan.Install[0].BinaryPath = repo, "binary", binary
	return plan, cfg, oldPath
}

func TestExecuteBlockerRemovalAndBinaryInstall(t *testing.T) {
	for _, blocker := range []string{"!", "!!"} {
		for _, jobs := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/jobs=%d", blocker, jobs), func(t *testing.T) {
				plan, cfg, oldPath := blockerRemovalFixture(t, blocker)
				cfg.Jobs = jobs
				var completed []string
				cfg.OnActionStart = func(_, total int, action resolve.PkgAction) {
					if total != 2 {
						t.Errorf("action total = %d", total)
					}
					if action.Action != "uninstall" {
						if _, err := os.Stat(oldPath); err != nil {
							t.Errorf("removed before build: %v", err)
						}
					}
				}
				cfg.OnActionComplete = func(_, total int, action resolve.PkgAction) {
					completed = append(completed, action.Action)
					if total != 2 {
						t.Errorf("action total = %d", total)
					}
					if action.Action == "uninstall" {
						remaining, err := resolve.LoadResume(cfg.ResumePath)
						if err != nil || len(remaining) != 1 {
							t.Errorf("requester checkpoint lost: %v %v", remaining, err)
						}
					}
				}
				if err := Execute(context.Background(), plan, cfg); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(completed, []string{"uninstall", "install"}) {
					t.Fatalf("commit order = %v", completed)
				}
				if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
					t.Fatalf("old VDB retained: %v", err)
				}
				if data, err := os.ReadFile(filepath.Join(cfg.Rebuild.RootDir, "usr/share/harness/current")); err != nil || string(data) != "new\n" {
					t.Fatalf("replacement payload = %q, %v", data, err)
				}
				if hooks, err := os.ReadFile(filepath.Join(cfg.Rebuild.RootDir, "hooks")); err != nil || string(hooks) != "prerm\npostrm\n" {
					t.Fatalf("installed hooks = %q, %v", hooks, err)
				}
				if remaining, err := resolve.LoadResume(cfg.ResumePath); err != nil || len(remaining) != 0 {
					t.Fatalf("checkpoint incomplete: %v %v", remaining, err)
				}
			})
		}
	}
}

func TestExecuteSourceBlockerRemovalBeforePreinst(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is unavailable")
	}
	plan, cfg, oldPath := blockerRemovalFixture(t, "!!")
	selected := &plan.Install[0]
	selected.MergeType, selected.BinaryPath = "source", ""
	packageDir := filepath.Join(selected.RepositoryPath, "virtual/perl-Test-Harness")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	recipe := `EAPI=8
SLOT=0/0
S="${WORKDIR}/${P}"
src_unpack() { mkdir -p "${S}"; }
src_install() {
	insinto /usr/share/harness
	echo new > "${T}/current"
	doins "${T}/current"
}
pkg_preinst() {
	[[ ! -d "${ROOT}/var/db/pkg/perl-core/Test-Harness-3.500.0" ]] || die "blocker still installed"
}
`
	if err := os.WriteFile(filepath.Join(packageDir, "perl-Test-Harness-3.520.0.ebuild"), []byte(recipe), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Execute(context.Background(), plan, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("blocker VDB retained: %v", err)
	}
	if payload, err := os.ReadFile(filepath.Join(cfg.Rebuild.RootDir, "usr/share/harness/current")); err != nil || string(payload) != "new\n" {
		t.Fatalf("source payload = %q, %v", payload, err)
	}
}

func TestBlockerRemovalCancellationRollsBackAndKeepsResume(t *testing.T) {
	plan, cfg, oldPath := blockerRemovalFixture(t, "!")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg.Rebuild.OnPhaseEnd = func(phase string, _ error) {
		if phase == "pkg_prerm" {
			cancel()
		}
	}
	err := Execute(ctx, plan, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("VDB not restored: %v", err)
	}
	if payload, err := os.ReadFile(filepath.Join(cfg.Rebuild.RootDir, "usr/share/harness/current")); err != nil || string(payload) != "old\n" {
		t.Fatalf("payload not restored: %q, %v", payload, err)
	}
	if remaining, err := resolve.LoadResume(cfg.ResumePath); err != nil || len(remaining) != 1 {
		t.Fatalf("checkpoint lost: %v, %v", remaining, err)
	}
}

func TestBlockerRemovalFailurePreservesRequesterCheckpoint(t *testing.T) {
	for _, failure := range []string{"build", "recovery", "removed", "record"} {
		t.Run(failure, func(t *testing.T) {
			plan, cfg, oldPath := blockerRemovalFixture(t, "!")
			cause := errors.New("injected " + failure + " failure")
			if failure == "recovery" {
				cfg.PrepareMutation = func(context.Context) error { return cause }
			}
			if failure == "record" {
				cfg.RecordLockedMutation = func() error { return cause }
			}
			cfg.Runner = func(ctx context.Context, _ string, packageCfg *rebuild.RebuildConfig) error {
				if failure == "build" {
					return cause
				}
				release, err := packageCfg.BeginMutation(ctx)
				if err != nil {
					return err
				}
				return errors.Join(cause, release())
			}
			err := Execute(context.Background(), plan, cfg)
			if !errors.Is(err, cause) {
				t.Fatalf("failure lost: %v", err)
			}
			if remaining, err := resolve.LoadResume(cfg.ResumePath); err != nil || len(remaining) != 1 {
				t.Fatalf("requester checkpoint lost: %v %v", remaining, err)
			}
			_, statErr := os.Stat(oldPath)
			removed := failure == "removed" || failure == "record"
			if removed != os.IsNotExist(statErr) {
				t.Fatalf("unexpected removal: removed=%t err=%v", removed, statErr)
			}
			// Re-resolve the pending requester against the resulting installed
			// state. A committed removal disappears; an untouched one is retried.
			graph := resolve.NewDepGraph()
			if !removed {
				old := graph.AddVersionFromRepository("perl-core/Test-Harness", "3.500.0", "0", "0", true, nil, "amd64", "test")
				old.InstalledEAPI, old.DependencyMetadataKnown = "8", true
			}
			parent := graph.AddVersionFromRepository("virtual/perl-Test-Harness", "3.520.0", "0", "0", false, nil, "amd64", "test")
			parent.Available, parent.DependencyMetadataKnown, parent.EAPI = true, true, "8"
			parent.Rdepend = "!<perl-core/Test-Harness-3.520.0"
			continued, err := resolve.Resolve(graph, []string{"=virtual/perl-Test-Harness-3.520.0"}, resolve.DefaultResolveConfig())
			wantRemovals := 1
			if removed {
				wantRemovals = 0
			}
			if err != nil || !continued.Verified || len(continued.Uninstall) != wantRemovals {
				t.Fatalf("resume plan: %#v %v", continued, err)
			}
			continued.Install[0].MergeType, continued.Install[0].BinaryPath, continued.Install[0].RepositoryPath = "binary", plan.Install[0].BinaryPath, plan.Install[0].RepositoryPath
			cfg.Runner, cfg.PrepareMutation, cfg.RecordLockedMutation = nil, nil, nil
			if err := Execute(context.Background(), continued, cfg); err != nil {
				t.Fatalf("resume execution: %v", err)
			}
		})
	}
}

func TestBlockerRemovalPreflightRejectsUnsafePlansBeforeBuild(t *testing.T) {
	for _, invalid := range []string{"missing requester", "missing binding", "duplicate", "wrong domain", "changed slot", "missing environment", "build only"} {
		t.Run(invalid, func(t *testing.T) {
			plan, cfg, oldPath := blockerRemovalFixture(t, "!")
			switch invalid {
			case "missing requester":
				plan.Uninstall[0].RemovalBefore = []string{"cat/absent"}
			case "missing binding":
				plan.Uninstall[0].RemovalBefore = nil
			case "duplicate":
				plan.Uninstall = append(plan.Uninstall, plan.Uninstall[0])
			case "wrong domain":
				plan.Uninstall[0].Domain = resolve.DomainBROOT
			case "changed slot":
				plan.Uninstall[0].Slot = "9"
			case "missing environment":
				if err := os.Remove(filepath.Join(oldPath, "environment.bz2")); err != nil {
					t.Fatal(err)
				}
			case "build only":
				cfg.Rebuild.BuildOnly = true
			}
			runs := 0
			cfg.Runner = func(context.Context, string, *rebuild.RebuildConfig) error { runs++; return nil }
			original := []byte("previous checkpoint")
			if err := os.WriteFile(cfg.ResumePath, original, 0644); err != nil {
				t.Fatal(err)
			}
			if err := Execute(context.Background(), plan, cfg); err == nil || runs != 0 {
				t.Fatalf("invalid plan ran: runs=%d err=%v", runs, err)
			}
			if data, err := os.ReadFile(cfg.ResumePath); err != nil || string(data) != string(original) {
				t.Fatalf("checkpoint overwritten: %q %v", data, err)
			}
		})
	}
}

func TestSharedBlockerRemovedOnceBeforeConcurrentRequesters(t *testing.T) {
	plan, cfg, oldPath := blockerRemovalFixture(t, "!")
	other := action(t, "app-misc/other-1")
	plan.Install = append(plan.Install, other)
	plan.Uninstall[0].RemovalBefore = append(plan.Uninstall[0].RemovalBefore, other.Atom.CP())
	cfg.Jobs = 2
	cfg.Preflight = func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil }
	var mu sync.Mutex
	removals := 0
	cfg.OnActionComplete = func(_, _ int, action resolve.PkgAction) {
		if action.Action == "uninstall" {
			mu.Lock()
			removals++
			mu.Unlock()
		}
	}
	cfg.Runner = func(ctx context.Context, _ string, packageCfg *rebuild.RebuildConfig) error {
		packageCfg.CommitLock.Lock()
		defer packageCfg.CommitLock.Unlock()
		release, err := packageCfg.BeginMutation(ctx)
		if err != nil {
			return err
		}
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Errorf("blocker present at merge: %v", err)
		}
		return errors.Join(packageCfg.OnTransactionCommit(nil), release())
	}
	if err := Execute(context.Background(), plan, cfg); err != nil {
		t.Fatal(err)
	}
	if removals != 1 {
		t.Fatalf("removal count = %d", removals)
	}
}
