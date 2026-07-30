package support

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasedCLIBugReportIsLocalPrivateAndReviewable(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build released CLI: %v\n%s", err, output)
	}
	outputDirectory := filepath.Join(root, "report")
	archive := filepath.Join(root, "report.tar.zst")
	command := exec.Command(binary, "bug-report",
		"--output", outputDirectory,
		"--archive", archive,
		"--latest-failure=false",
		"--package", "cat/pkg",
	)
	command.Env = append(os.Environ(),
		"HOME=/home/report-user",
		"HOSTNAME=private-host",
		"ROOT="+root,
		"PORTAGE_TMPDIR="+root,
		"DISTDIR="+root,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("released bug-report: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Nothing was uploaded.") {
		t.Fatalf("missing local-only confirmation: %s", output)
	}
	data, err := os.ReadFile(filepath.Join(outputDirectory, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Schema  int    `json:"schema"`
		Package string `json:"package"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != 1 || report.Package != "cat/pkg" {
		t.Fatalf("report contract=%#v", report)
	}
	for _, path := range []string{outputDirectory, filepath.Join(outputDirectory, "report.json"), filepath.Join(outputDirectory, "report.md"), archive} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%#o, want %#o", path, info.Mode().Perm(), want)
		}
	}
}
