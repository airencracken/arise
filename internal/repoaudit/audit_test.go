package repoaudit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunReportsRepositoryCompatibilitySurfaces(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "eclass"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repository, "eclass", "base.eclass"), "inherit missing\nprobe() { has_version \"dev-lang/rust:${slot}\"; }\n")
	write(filepath.Join(repository, "cat", "pkg", "pkg-1.ebuild"), "EAPI=8\nif true; then\n inherit base\nfi\nsrc_install() { dobin pkg; }\n")
	worker := filepath.Join(repository, "worker.sh")
	write(worker, "has_version() { :; }\ndobin() { :; }\n")

	report, err := Run(repository, worker)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ebuilds != 1 || report.Eclasses != 1 || report.EAPIs["8"] != 1 {
		t.Fatalf("unexpected counts: %#v", report)
	}
	if len(report.MissingInherits) != 1 || report.MissingInherits[0].Eclass != "missing" {
		t.Fatalf("missing inherits: %#v", report.MissingInherits)
	}
	if report.QueryClasses["variable"] != 1 {
		t.Fatalf("query classes: %#v", report.QueryClasses)
	}
	if len(report.InheritDifferences) == 0 {
		t.Fatal("expected parser/static inheritance difference")
	}
}

func TestFindCycles(t *testing.T) {
	cycles := findCycles(map[string][]string{"a": {"b"}, "b": {"a"}})
	if len(cycles) != 1 || len(cycles[0]) != 3 {
		t.Fatalf("cycles = %#v", cycles)
	}
}
