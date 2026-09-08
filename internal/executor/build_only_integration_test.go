package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

func TestBuildOnlyRealWorkersPublishWithoutInstalling(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	for _, jobs := range []int{1, 2} {
		t.Run(map[int]string{1: "serial", 2: "parallel"}[jobs], func(t *testing.T) {
			base := t.TempDir()
			repo := filepath.Join(base, "repo")
			root := filepath.Join(base, "root")
			vdb := filepath.Join(root, "var/db/pkg")
			for _, dir := range []string{filepath.Join(repo, "eclass"), vdb, filepath.Join(base, "logs"), filepath.Join(base, "journal"), filepath.Join(base, "work")} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			var actions []resolve.PkgAction
			for _, name := range []string{"first", "second"} {
				dir := filepath.Join(repo, "app-misc", name)
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				body := "EAPI=8\nSLOT=0\nS=\"${WORKDIR}/${P}\"\nsrc_unpack() { mkdir -p \"${S}\"; }\nsrc_install() { insinto /usr/share/audit; echo archive > \"${T}/" + name + "\"; doins \"${T}/" + name + "\"; }\n"
				if err := os.WriteFile(filepath.Join(dir, name+"-1.ebuild"), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
				selected := action(t, "app-misc/"+name+"-1")
				selected.RepositoryPath = repo
				selected.Slot = "0"
				actions = append(actions, selected)
			}
			resume := filepath.Join(base, "resume")
			cfg := Config{Jobs: jobs, ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root, VdbDir: vdb, WorkDirBase: filepath.Join(base, "work"), PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journal"), PackageDir: filepath.Join(base, "packages"), PhaseProtocol: true, BuildOnly: true, BuildPackage: true, Repositories: []portage.RepoEntry{{Name: "test", Location: repo}}}}
			if err := Execute(context.Background(), &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: actions}, cfg); err != nil {
				t.Fatal(err)
			}
			index, err := binpkg.ReadPackagesIndex(filepath.Join(cfg.Rebuild.PackageDir, "Packages"))
			if err != nil || len(index.Packages) != 2 {
				t.Fatalf("archives not indexed: %v %v", index, err)
			}
			if _, err := os.Stat(filepath.Join(root, "usr")); !os.IsNotExist(err) {
				t.Fatalf("build-only installed payload: %v", err)
			}
			entries, err := os.ReadDir(vdb)
			if err != nil || len(entries) != 0 {
				t.Fatalf("build-only changed VDB: %v %v", entries, err)
			}
			remaining, err := resolve.LoadResume(resume)
			if err != nil || len(remaining) != 0 {
				t.Fatalf("archive resume incomplete: %v %v", remaining, err)
			}
		})
	}
}
