package main

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/resolve"
)

func TestFetchPlanActionsUsesVerifiedCachedArtifact(t *testing.T) {
	repository := t.TempDir()
	distdir := t.TempDir()
	packageDir := filepath.Join(repository, "app-misc", "cached")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("offline")
	digest := sha512.Sum512(content)
	manifest := "DIST source.tar 7 SHA512 " + hex.EncodeToString(digest[:]) + "\n"
	if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distdir, "source.tar"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	action := resolve.PkgAction{Atom: mustFetchAtom(t, "app-misc/cached-1"), RepositoryPath: repository, SrcURI: "https://invalid.example/source.tar", MergeType: "source"}
	if err := fetchPlanActions(context.Background(), []resolve.PkgAction{action}, fetch.FetchConfig{DistfilesDir: distdir}, &fetch.Fetcher{}); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPlanActionsRefusesMissingManifestAndBinaryTransport(t *testing.T) {
	action := resolve.PkgAction{Atom: mustFetchAtom(t, "app-misc/missing-1"), RepositoryPath: t.TempDir(), SrcURI: "https://invalid.example/source.tar", MergeType: "source"}
	if err := fetchPlanActions(context.Background(), []resolve.PkgAction{action}, fetch.FetchConfig{DistfilesDir: t.TempDir()}, &fetch.Fetcher{}); err == nil || !strings.Contains(err.Error(), "open Manifest") {
		t.Fatalf("missing Manifest error = %v", err)
	}
	action.MergeType = "binary"
	if err := fetchPlanActions(context.Background(), []resolve.PkgAction{action}, fetch.FetchConfig{DistfilesDir: t.TempDir()}, &fetch.Fetcher{}); err == nil || !strings.Contains(err.Error(), "binary acquisition") {
		t.Fatalf("binary error = %v", err)
	}
}

func TestPlanExecutionRequiresWholeStateVerification(t *testing.T) {
	if err := planExecutionVerificationError(&resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}); err != nil {
		t.Fatalf("verified plan rejected: %v", err)
	}
	for _, result := range []*resolve.ResolveResult{nil, {}, {Verification: resolve.VerificationSkippedNoDeps}} {
		if err := planExecutionVerificationError(result); err == nil {
			t.Fatalf("unverified plan accepted: %#v", result)
		}
	}
}

func mustFetchAtom(t *testing.T, value string) *atom.Atom {
	t.Helper()
	parsed, err := atom.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
