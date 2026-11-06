package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func writeMutationTestPackage(t *testing.T, root, cpv, rdepend string) {
	t.Helper()
	if err := writeMutationPackage(root, cpv, rdepend); err != nil {
		t.Fatal(err)
	}
}

func writeMutationPackage(root, cpv, rdepend string) error {
	path := filepath.Join(root, cpv)
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	for name, value := range map[string]string{"SLOT": "0", "EAPI": "8", "repository": "gentoo", "CONTENTS": "", "RDEPEND": rdepend, "IUSE": "", "USE": ""} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0644); err != nil {
			return err
		}
	}
	return nil
}

func newMutationTestGuard(t *testing.T) (*packageMutationGuard, *resolve.ResolveResult) {
	t.Helper()
	return newMutationTestGuardWithDependency(t, false)
}

func newMutationTestGuardWithDependency(t *testing.T, upgrade bool) (*packageMutationGuard, *resolve.ResolveResult) {
	t.Helper()
	root := t.TempDir()
	guard := &packageMutationGuard{vdbDir: filepath.Join(root, "vdb"), worldPath: filepath.Join(root, "world"), configRoot: filepath.Join(root, "config"), output: io.Discard}
	if err := os.MkdirAll(guard.configRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard.worldPath, []byte("media-video/ffmpeg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeMutationTestPackage(t, guard.vdbDir, "dev-libs/codec-1", "")
	writeMutationTestPackage(t, guard.vdbDir, "media-video/ffmpeg-7", "dev-libs/codec")
	graph := resolve.NewDepGraph()
	for _, cpv := range []string{"dev-libs/codec-1", "media-video/ffmpeg-7"} {
		parsed, err := atom.ParsePackageAtom("=" + cpv)
		if err != nil {
			t.Fatal(err)
		}
		installed := graph.AddVersionFromRepository(parsed.CP(), parsed.Version.Raw, "0", "0", true, nil, "amd64", "gentoo")
		installed.Available, installed.DependencyMetadataKnown = true, true
		installed.EAPI, installed.InstalledEAPI = "8", "8"
		if parsed.CP() == "media-video/ffmpeg" {
			installed.InstalledRdepend = "dev-libs/codec"
		}
	}
	candidate := graph.AddVersionFromRepository("media-video/ffmpeg", "8", "0", "0", false, nil, "amd64", "gentoo")
	candidate.DependencyMetadataKnown, candidate.EAPI, candidate.Rdepend = true, "8", "dev-libs/codec"
	wantActions := 1
	if upgrade {
		provider := graph.AddVersionFromRepository("dev-libs/codec", "2", "0", "0", false, nil, "amd64", "gentoo")
		provider.DependencyMetadataKnown, provider.EAPI = true, "8"
		candidate.Rdepend = ">=dev-libs/codec-2"
		wantActions = 2
	}
	config := resolve.DefaultResolveConfig()
	config.Update = true
	result, err := resolve.Resolve(graph, []string{"media-video/ffmpeg"}, config)
	if err != nil || !result.Verified || len(result.Install) != wantActions {
		t.Fatalf("fixture resolution: %#v, %v", result, err)
	}
	guard.audit, err = prepareIndependentPlanAudit(graph, result, []string{"media-video/ffmpeg"}, config)
	if err != nil {
		t.Fatal(err)
	}
	guard.actions = result.Install
	guard.snapshot, err = captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
	if err != nil {
		t.Fatal(err)
	}
	return guard, result
}

func TestMutationGuardAllowsUnrelatedConcurrentInstall(t *testing.T) {
	guard, _ := newMutationTestGuard(t)
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	writeMutationTestPackage(t, guard.vdbDir, "www-apps/comfyware-1", "dev-libs/codec")
	if err := os.WriteFile(guard.worldPath, []byte("media-video/ffmpeg\nwww-apps/comfyware\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err != nil {
		t.Fatalf("unrelated install stopped a valid prepared merge: %v", err)
	}
}

func TestMutationGuardRejectsRelevantChangesWithoutAdvancingBaseline(t *testing.T) {
	for _, name := range []string{"dependency USE", "dependency removal", "dependency rebuild", "planned package", "policy", "environment"} {
		t.Run(name, func(t *testing.T) {
			guard, _ := newMutationTestGuard(t)
			if err := guard.validate(); err != nil {
				t.Fatal(err)
			}
			before := guard.snapshot.sha256
			var path, content string
			switch name {
			case "dependency USE":
				path, content = filepath.Join(guard.vdbDir, "dev-libs/codec-1/USE"), "changed"
			case "dependency removal":
				if err := os.RemoveAll(filepath.Join(guard.vdbDir, "dev-libs/codec-1")); err != nil {
					t.Fatal(err)
				}
			case "dependency rebuild":
				path, content = filepath.Join(guard.vdbDir, "dev-libs/codec-1/COUNTER"), "2"
			case "planned package":
				path, content = filepath.Join(guard.vdbDir, "media-video/ffmpeg-7/COUNTER"), "2"
			case "policy":
				path, content = filepath.Join(guard.configRoot, "make.conf"), "USE=test\n"
			case "environment":
				t.Setenv("USE", os.Getenv("USE")+" mutation-guard-test")
			}
			if path != "" {
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := guard.validate(); err == nil {
				t.Fatal("relevant change was silently accepted")
			}
			if guard.snapshot.sha256 != before {
				t.Fatal("failed validation advanced the trusted baseline")
			}
		})
	}
}

func TestMutationGuardRejectsNewReverseDependencyAndBlocker(t *testing.T) {
	for _, dependency := range []string{"<media-video/ffmpeg-8", "!>=media-video/ffmpeg-8", "media-video/ffmpeg:0/7="} {
		t.Run(dependency, func(t *testing.T) {
			guard, _ := newMutationTestGuard(t)
			if dependency == "media-video/ffmpeg:0/7=" {
				if err := os.WriteFile(filepath.Join(guard.vdbDir, "media-video/ffmpeg-7/SLOT"), []byte("0/7"), 0644); err != nil {
					t.Fatal(err)
				}
				for index := range guard.audit.fixture.Installed {
					if guard.audit.fixture.Installed[index].CPV == "media-video/ffmpeg-7" {
						guard.audit.fixture.Installed[index].Subslot = "7"
					}
				}
				guard.audit.plan.Actions[0].Package.Subslot = "8"
				for index := range guard.audit.fixture.Available {
					if guard.audit.fixture.Available[index].CPV == "media-video/ffmpeg-8" {
						guard.audit.fixture.Available[index].Subslot = "8"
					}
				}
				var err error
				guard.snapshot, err = captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := guard.validate(); err != nil {
				t.Fatal(err)
			}
			before := guard.snapshot.sha256
			writeMutationTestPackage(t, guard.vdbDir, "www-apps/comfyware-1", dependency)
			if err := guard.validate(); err == nil || !strings.Contains(err.Error(), "invalidate the remaining plan") {
				t.Fatalf("unsafe new consumer was accepted: %v", err)
			}
			if guard.snapshot.sha256 != before {
				t.Fatal("unsafe state was adopted")
			}
			if _, err := os.Stat(filepath.Join(guard.vdbDir, "media-video/ffmpeg-7/CONTENTS")); err != nil {
				t.Fatal("validation changed installed ffmpeg")
			}
		})
	}
}

func TestMutationGuardAcceptsWorldBookkeepingAndUnrelatedRemoval(t *testing.T) {
	guard, _ := newMutationTestGuard(t)
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard.worldPath, []byte("www-apps/comfyware\nmedia-video/ffmpeg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err != nil {
		t.Fatalf("world addition invalidated package plan: %v", err)
	}
	writeMutationTestPackage(t, guard.vdbDir, "www-apps/comfyware-1", "dev-libs/codec")
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(guard.vdbDir, "www-apps/comfyware-1")); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err != nil {
		t.Fatalf("unrelated removal invalidated package plan: %v", err)
	}
	world, err := os.ReadFile(guard.worldPath)
	if err != nil || string(world) != "www-apps/comfyware\nmedia-video/ffmpeg\n" {
		t.Fatal("revalidation overwrote another install's world selection")
	}
}

func TestMutationGuardDoesNotReplayCommittedActions(t *testing.T) {
	guard, result := newMutationTestGuard(t)
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(guard.vdbDir, "media-video/ffmpeg-7")); err != nil {
		t.Fatal(err)
	}
	writeMutationTestPackage(t, guard.vdbDir, "media-video/ffmpeg-8", "dev-libs/codec")
	if err := guard.record(); err != nil {
		t.Fatal(err)
	}
	guard.complete(result.Install[0])
	writeMutationTestPackage(t, guard.vdbDir, "www-apps/comfyware-1", "media-video/ffmpeg")
	if err := guard.validate(); err != nil {
		t.Fatalf("completed action was replayed against its installed replacement: %v", err)
	}
}

func TestMutationGuardAcceptsMetadataCacheChanges(t *testing.T) {
	guard, _ := newMutationTestGuard(t)
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(guard.vdbDir, ".cache")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "dep"), []byte("regenerated"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err != nil {
		t.Fatalf("derived VDB cache invalidated package plan: %v", err)
	}
}

func TestMutationGuardRejectsChangedInvalidVDBMetadata(t *testing.T) {
	guard, _ := newMutationTestGuard(t)
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	writeMutationTestPackage(t, guard.vdbDir, "www-apps/comfyware-1", "")
	if err := os.Remove(filepath.Join(guard.vdbDir, "www-apps/comfyware-1/CONTENTS")); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err == nil || !strings.Contains(err.Error(), "metadata cannot be revalidated") {
		t.Fatalf("new incomplete VDB record was silently ignored: %v", err)
	}
}

func TestMutationGuardTracksSymlinkedDatabase(t *testing.T) {
	guard, _ := newMutationTestGuard(t)
	link := filepath.Join(filepath.Dir(guard.vdbDir), "vdb-link")
	if err := os.Symlink(guard.vdbDir, link); err != nil {
		t.Fatal(err)
	}
	guard.vdbDir = link
	var err error
	guard.snapshot, err = captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(link, "dev-libs/codec-1/COUNTER"), []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err == nil {
		t.Fatal("dependency change through a symlinked VDB was hidden")
	}
}

func TestMutationGuardRevalidatesAfterCommittedPrerequisite(t *testing.T) {
	guard, result := newMutationTestGuardWithDependency(t, true)
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(guard.audit.plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Install[0].Atom.CP() != "dev-libs/codec" {
		t.Fatal("dependency was not first")
	}
	if err := os.RemoveAll(filepath.Join(guard.vdbDir, "dev-libs/codec-1")); err != nil {
		t.Fatal(err)
	}
	writeMutationTestPackage(t, guard.vdbDir, "dev-libs/codec-2", "")
	if err := guard.record(); err != nil {
		t.Fatal(err)
	}
	guard.complete(result.Install[0])
	writeMutationTestPackage(t, guard.vdbDir, "www-apps/comfyware-1", "dev-libs/codec")
	if err := guard.validate(); err != nil {
		t.Fatalf("completed prerequisite invalidated remaining action/decision replay: %v", err)
	}
	after, err := json.Marshal(guard.audit.plan)
	if err != nil || string(after) != string(original) {
		t.Fatal("revalidation mutated the approved plan evidence")
	}
}

func TestMutationGuardStillBindsSelectedSources(t *testing.T) {
	guard, _ := newMutationTestGuard(t)
	repo := filepath.Join(filepath.Dir(guard.vdbDir), "repo")
	path := filepath.Join(repo, "media-video/ffmpeg/ffmpeg-8.ebuild")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("EAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}
	guard.actions[0].RepositoryPath = repo
	var err error
	guard.snapshot, err = captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("EAPI=8\nRDEPEND=dev-libs/other\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := guard.validate(); err == nil || !strings.Contains(err.Error(), "selected sources changed") {
		t.Fatalf("changed recipe accepted for a prepared image: %v", err)
	}
}
