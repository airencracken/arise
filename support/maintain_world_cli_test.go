package support

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasedCLIMaintainWorldFixRepairsWithoutPlanApproval(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	buildOutput, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build release CLI: %v\n%s", err, buildOutput)
	}

	worldPath := filepath.Join(root, "world")
	vdbRoot := filepath.Join(root, "vdb")
	repoRoot := filepath.Join(root, "repo")
	configRoot := filepath.Join(root, "etc", "portage")
	for _, directory := range []string{
		vdbRoot,
		filepath.Join(repoRoot, "metadata", "md5-cache"),
		configRoot,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(worldPath, []byte("cat/missing\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(
		binary,
		"--world-file", worldPath,
		"--vdb-dir", vdbRoot,
		"--repo", repoRoot,
		"--portage-config-root", configRoot,
		"maintain", "world", "--fix",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("arise maintain world --fix: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "execution requires --approve-plan") {
		t.Fatalf("repair unexpectedly required plan approval:\n%s", output)
	}
	if !strings.Contains(string(output), "Repaired world file: 1 action(s) applied.") {
		t.Fatalf("repair output=%q", output)
	}
	world, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(world) != 0 {
		t.Fatalf("world after repair=%q", world)
	}
}
