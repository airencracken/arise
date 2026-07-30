package support

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReleasedCLIMaintainMoveInstHonorsAlternateRoot(t *testing.T) {
	base := t.TempDir()
	binary := filepath.Join(base, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build released CLI: %v\n%s", err, output)
	}
	targetRoot := filepath.Join(base, "target")
	configBase := filepath.Join(base, "config")
	repoRoot := filepath.Join(base, "repo")
	vdbEntry := filepath.Join(targetRoot, "var", "db", "pkg", "cat", "old-1")
	for _, directory := range []string{
		vdbEntry, filepath.Join(configBase, "etc", "portage"),
		filepath.Join(repoRoot, "profiles", "updates"), filepath.Join(repoRoot, "metadata"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(vdbEntry, "CONTENTS"):                       "",
		filepath.Join(vdbEntry, "EAPI"):                           "8\n",
		filepath.Join(vdbEntry, "SLOT"):                           "0\n",
		filepath.Join(vdbEntry, "repository"):                     "gentoo\n",
		filepath.Join(vdbEntry, "BUILD_TIME"):                     "100\n",
		filepath.Join(vdbEntry, "RDEPEND"):                        "cat/old\n",
		filepath.Join(repoRoot, "profiles", "repo_name"):          "gentoo\n",
		filepath.Join(repoRoot, "profiles", "updates", "1Q-2026"): "move cat/old new/pkg\n",
		filepath.Join(repoRoot, "metadata", "layout.conf"):        "repo-name = gentoo\n",
		filepath.Join(configBase, "etc", "portage", "repos.conf"): "[DEFAULT]\nmain-repo = gentoo\n[gentoo]\nlocation = " + repoRoot + "\n",
	}
	for path, value := range files {
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(binary, "maintain", "moveinst", "--fix")
	command.Env = append(os.Environ(),
		"ROOT="+targetRoot,
		"PORTAGE_CONFIGROOT="+configBase,
		"PORTDIR="+repoRoot,
		"PORTAGE_TMPDIR="+filepath.Join(targetRoot, "var", "tmp"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("alternate-root moveinst: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "var", "db", "pkg", "new", "pkg-1")); err != nil {
		t.Fatalf("target root was not updated: %v", err)
	}
}
