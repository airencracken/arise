package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/mergehistory"
	"github.com/airencracken/arise/internal/resolve"
)

func TestLoadMergeEstimatesUsesMedianPortageHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emerge.log")
	content := "100:  >>> emerge (1 of 1) llvm-core/llvm-19.1.7::gentoo to /\n" +
		"160:  ::: completed emerge (1 of 1) llvm-core/llvm-19.1.7::gentoo to /\n" +
		"200:  >>> emerge (1 of 1) llvm-core/llvm-21.1.8::gentoo to /\n" +
		"320:  ::: completed emerge (1 of 1) llvm-core/llvm-21.1.8::gentoo to /\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := historicalEstimateForTest(t, path, "llvm-core/llvm-22"); got != 90*time.Second {
		t.Fatalf("median estimate = %v, want 1m30s", got)
	}
}

func TestLoadMergeEstimatesRecoversNULPaddedHistoricalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emerge.log")
	content := "100:  >>> emerge (1 of 1) app-misc/example-1::gentoo to /\n" +
		"\x00\x00\x00160:  ::: completed emerge (1 of 1) app-misc/example-1::gentoo to /\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := historicalEstimateForTest(t, path, "app-misc/example-1"); got != time.Minute {
		t.Fatalf("recovered estimate = %v, want 1m", got)
	}
}

func TestPortageMergeLogProjectionRoundTripsIntoEstimate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emerge.log")
	log, err := openPortageMergeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := atom.Parse("app-misc/example-1")
	action := resolve.PkgAction{Atom: a, Repository: "gentoo"}
	if err := log.event(false, 1, 1, action); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := log.event(true, 1, 1, action); err != nil {
		t.Fatal(err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	if got := historicalEstimateForTest(t, path, "app-misc/example-1"); got < time.Second || got > 2*time.Second {
		t.Fatalf("projected estimate = %v", got)
	}
}

func TestOpenPortageMergeLogRejectsNULCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emerge.log")
	if err := os.WriteFile(path, []byte("100: valid\n\x00\x00"+"200: damaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := openPortageMergeLog(path)
	if err == nil {
		_ = log.close()
		t.Fatal("NUL-corrupted timing log was accepted for append")
	}
	if !strings.Contains(err.Error(), "contains NUL bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlockerRemovalsAreLoggedWithoutBuildEstimates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emerge.log")
	log, err := openPortageMergeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := atom.Parse("perl-core/Test-Harness-3.500.0")
	if err != nil {
		t.Fatal(err)
	}
	action := resolve.PkgAction{Atom: a, Action: "uninstall", Repository: "gentoo"}
	if err := log.event(false, 2, 2, action); err != nil {
		t.Fatal(err)
	}
	if err := log.event(true, 2, 2, action); err != nil {
		t.Fatal(err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"=== Unmerging... (perl-core/Test-Harness-3.500.0)", ">>> unmerge success: perl-core/Test-Harness-3.500.0"} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("missing removal event %q:\n%s", text, data)
		}
	}
	if _, known := loadMergeEstimates(path).forAction(action); known {
		t.Fatal("removal time was counted as a build estimate")
	}
}

func historicalEstimateForTest(t *testing.T, log, cpv string) time.Duration {
	t.Helper()
	a, err := atom.Parse(cpv)
	if err != nil {
		t.Fatal(err)
	}
	estimate, known := loadMergeEstimates(log).forAction(resolve.PkgAction{Atom: a})
	if !known {
		t.Fatal("missing historical estimate")
	}
	return estimate
}

func TestPortageMergeLogRetainsSelectedRoot(t *testing.T) {
	t.Setenv("ROOT", "/prefix with spaces")
	path := filepath.Join(t.TempDir(), "emerge.log")
	log, err := openPortageMergeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	action := resolve.PkgAction{Atom: planTestAtom(t, "cat/pkg-1"), Action: "install"}
	if err := log.event(false, 1, 1, action); err != nil {
		t.Fatal(err)
	}
	if err := log.event(true, 1, 1, action); err != nil {
		t.Fatal(err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	report, err := mergehistory.Read(context.Background(), []string{path})
	if err != nil || len(report.Events) != 1 || report.Events[0].Root != "/prefix with spaces" {
		t.Fatalf("root lost: %+v %v", report, err)
	}
}
