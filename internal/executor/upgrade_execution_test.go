package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

func TestExecuteRealSourceAndBinaryUpgradeIntoDisposableRoot(t *testing.T) {
	for _, lane := range []string{"source", "binary"} {
		for _, jobs := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/jobs=%d", lane, jobs), func(t *testing.T) {
				if lane == "source" {
					if _, err := exec.LookPath("sandbox"); err != nil {
						t.Skip("Portage sandbox is unavailable")
					}
				}
				base := t.TempDir()
				repo, root := filepath.Join(base, "repo"), filepath.Join(base, "root")
				for _, dir := range []string{filepath.Join(repo, "eclass"), filepath.Join(repo, "app-misc/demo"), root} {
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
				}
				cfg := Config{Jobs: jobs, ResumePath: filepath.Join(base, "resume"), Rebuild: rebuild.RebuildConfig{
					RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"), WorkDirBase: filepath.Join(base, "work"),
					PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
					PhaseProtocol: true, Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
				}}
				for step, version := range []string{"1", "2", "2"} {
					selected := action(t, "app-misc/demo-"+version)
					selected.Slot, selected.RepositoryPath, selected.MergeType = "0", repo, lane
					if step == 1 {
						selected.Action, selected.InstalledVersion = "update", "1"
					} else if step == 2 {
						selected.Action, selected.InstalledVersion = "reinstall", "2"
					}
					if lane == "source" {
						body := fmt.Sprintf("EAPI=8\nSLOT=0\nS=\"${WORKDIR}/${P}\"\nsrc_unpack() { mkdir -p \"${S}\"; }\nsrc_install() { insinto /usr/share/demo; echo %s > \"${T}/current\"; doins \"${T}/current\"; newins \"${T}/current\" version-%s; }\n", version, version)
						if err := os.WriteFile(filepath.Join(repo, "app-misc/demo/demo-"+version+".ebuild"), []byte(body), 0644); err != nil {
							t.Fatal(err)
						}
					} else {
						image := filepath.Join(base, "image-"+version)
						if err := os.MkdirAll(filepath.Join(image, "usr/share/demo"), 0755); err != nil {
							t.Fatal(err)
						}
						for _, name := range []string{"current", "version-" + version} {
							if err := os.WriteFile(filepath.Join(image, "usr/share/demo", name), []byte(version+"\n"), 0644); err != nil {
								t.Fatal(err)
							}
						}
						selected.BinaryPath = filepath.Join(base, "demo-"+version+".gpkg.tar")
						if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{
							Path: selected.BinaryPath, Basename: "demo-" + version, ImageRoot: image,
							Metadata: map[string][]byte{"CATEGORY": []byte("app-misc"), "PF": []byte("demo-" + version), "SLOT": []byte("0"), "EAPI": []byte("8")},
						}); err != nil {
							t.Fatal(err)
						}
					}
					plan := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{selected}}
					if err := Execute(context.Background(), plan, cfg); err != nil {
						t.Fatalf("step %d (%s %s): %v", step+1, selected.Action, version, err)
					}
					if payload, err := os.ReadFile(filepath.Join(root, "usr/share/demo/current")); err != nil || string(payload) != version+"\n" {
						t.Fatalf("step %d payload = %q, %v", step+1, payload, err)
					}
					remaining, err := resolve.LoadResume(cfg.ResumePath)
					if err != nil || len(remaining) != 0 {
						t.Fatalf("committed checkpoint = %v, %v", remaining, err)
					}
				}
				for _, obsolete := range []string{filepath.Join(cfg.Rebuild.VdbDir, "app-misc/demo-1"), filepath.Join(root, "usr/share/demo/version-1")} {
					if _, err := os.Lstat(obsolete); !os.IsNotExist(err) {
						t.Fatalf("upgrade retained %s: %v", obsolete, err)
					}
				}
			})
		}
	}
}
