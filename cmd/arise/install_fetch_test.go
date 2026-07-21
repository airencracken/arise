package main

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	if err := fetchPlanActions(context.Background(), []resolve.PkgAction{action}, fetch.FetchConfig{DistfilesDir: distdir}, &fetch.Fetcher{}, 4); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPlanActionsRefusesMissingManifestAndBinaryTransport(t *testing.T) {
	action := resolve.PkgAction{Atom: mustFetchAtom(t, "app-misc/missing-1"), RepositoryPath: t.TempDir(), SrcURI: "https://invalid.example/source.tar", MergeType: "source"}
	if err := fetchPlanActions(context.Background(), []resolve.PkgAction{action}, fetch.FetchConfig{DistfilesDir: t.TempDir()}, &fetch.Fetcher{}, 4); err == nil || !strings.Contains(err.Error(), "open Manifest") {
		t.Fatalf("missing Manifest error = %v", err)
	}
	action.MergeType = "binary"
	if err := fetchPlanActions(context.Background(), []resolve.PkgAction{action}, fetch.FetchConfig{DistfilesDir: t.TempDir()}, &fetch.Fetcher{}, 4); err == nil || !strings.Contains(err.Error(), "binary acquisition") {
		t.Fatalf("binary error = %v", err)
	}
}

func TestFetchPlanActionsRunsIndependentDownloadsConcurrently(t *testing.T) {
	content := []byte("payload")
	digest := sha512.Sum512(content)
	var active, maximum atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		if current >= 2 {
			releaseOnce.Do(func() { close(release) })
		}
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		_, _ = writer.Write(content)
	}))
	defer server.Close()

	repository := t.TempDir()
	var actions []resolve.PkgAction
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("source-%d.tar", index)
		packageName := fmt.Sprintf("pkg%d", index)
		packageDir := filepath.Join(repository, "app-misc", packageName)
		if err := os.MkdirAll(packageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf("DIST %s %d SHA512 %s\n", name, len(content), hex.EncodeToString(digest[:]))
		if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, resolve.PkgAction{
			Atom: mustFetchAtom(t, fmt.Sprintf("app-misc/%s-1", packageName)), RepositoryPath: repository,
			SrcURI: server.URL + "/" + name, MergeType: "source",
		})
	}
	if err := fetchPlanActions(context.Background(), actions, fetch.FetchConfig{DistfilesDir: t.TempDir()}, &fetch.Fetcher{}, 2); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() < 2 {
		t.Fatalf("maximum concurrent downloads = %d, want at least 2", maximum.Load())
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
