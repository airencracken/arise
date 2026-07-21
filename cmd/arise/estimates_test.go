package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
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
	if got := loadMergeEstimates(path)["llvm-core/llvm"]; got != 120*time.Second {
		t.Fatalf("median estimate = %v, want 2m", got)
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
	if got := loadMergeEstimates(path)["app-misc/example"]; got < time.Second || got > 2*time.Second {
		t.Fatalf("projected estimate = %v", got)
	}
}
