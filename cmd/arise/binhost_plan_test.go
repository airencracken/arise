package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
)

func TestRemoteBinaryPlanningProcess(t *testing.T) {
	root := os.Getenv("ARISE_REMOTE_BINARY_TEST_ROOT")
	if root == "" {
		return
	}
	*vdbDir, *worldFile = filepath.Join(root, "var/db/pkg"), filepath.Join(root, "world")
	*portageConfigRoot, *resumeFile = filepath.Join(root, "config"), filepath.Join(root, "resume")
	*binpkgDir, *workDir, *journalDir = filepath.Join(root, "packages"), filepath.Join(root, "work"), filepath.Join(root, "journal")
	*emergeLog = filepath.Join(root, "merge.log")
	*getbinpkgOnly = true
	*pretend = os.Getenv("ARISE_REMOTE_BINARY_TEST_MODE") == "pretend"
	*ask = !*pretend
	runInstall([]string{"app-misc/demo"}, filepath.Join(root, "db"), filepath.Join(root, "repo"))
	os.Exit(0)
}

func TestRemoteBinaryPlanningAcquiresOnlyAfterConfirmation(t *testing.T) {
	for _, mode := range []string{"pretend", "decline", "execute"} {
		t.Run(mode, func(t *testing.T) {
			root, hostRoot := t.TempDir(), t.TempDir()
			for _, dir := range []string{"config", "repo", "var/db/pkg"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			index := &binpkg.PackagesIndex{Header: map[string]string{"VERSION": "0"}}
			entries := make(chan *metadata.PackageMetadata, 2)
			for _, name := range []string{"demo", "dependency"} {
				image := filepath.Join(hostRoot, "image-"+name)
				if err := os.MkdirAll(filepath.Join(image, "usr/share/audit"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(image, "usr/share/audit", name), []byte(name), 0644); err != nil {
					t.Fatal(err)
				}
				archive := filepath.Join(hostRoot, name+"-1.gpkg.tar")
				meta := map[string][]byte{"CATEGORY": []byte("app-misc"), "PF": []byte(name + "-1"), "SLOT": []byte("0"), "EAPI": []byte("8"), "repository": []byte("test")}
				record := &metadata.PackageMetadata{Category: "app-misc", Package: name, Version: "1", SLOT: "0", EAPI: "8", KEYWORDS: "amd64", Repository: "test", RepositoryPath: filepath.Join(root, "repo")}
				if name == "demo" {
					record.RDEPEND, record.BDEPEND = "app-misc/dependency", "dev-build/unavailable-source-tool"
					meta["RDEPEND"] = []byte(record.RDEPEND)
				}
				entries <- record
				if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{Path: archive, Basename: name + "-1", ImageRoot: image, Metadata: meta}); err != nil {
					t.Fatal(err)
				}
				info, err := binpkg.ReadInfo(archive)
				if err != nil {
					t.Fatal(err)
				}
				entry, err := binpkg.NewPackageIndexEntry(archive, hostRoot, info, meta)
				if err != nil {
					t.Fatal(err)
				}
				index.Packages = append(index.Packages, entry)
			}
			close(entries)
			if err := binpkg.WritePackagesIndex(filepath.Join(hostRoot, "Packages"), index, time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}
			db, err := ingest.OpenDB(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ingest.Ingest(db, entries); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			var indexRequests, archiveRequests atomic.Int32
			files := http.FileServer(http.Dir(hostRoot))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/Packages" {
					indexRequests.Add(1)
				} else {
					archiveRequests.Add(1)
				}
				files.ServeHTTP(w, r)
			}))
			defer server.Close()
			if err := os.WriteFile(filepath.Join(root, "config/make.conf"), []byte("ARCH=amd64\nACCEPT_KEYWORDS=amd64\nPORTAGE_BINHOST=\""+server.URL+"\"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestRemoteBinaryPlanningProcess$")
			command.Env = append(os.Environ(), "ARISE_REMOTE_BINARY_TEST_ROOT="+root, "ARISE_REMOTE_BINARY_TEST_MODE="+mode, "ROOT="+root)
			answer := "no\n"
			if mode == "execute" {
				answer = "yes\n"
			}
			command.Stdin = strings.NewReader(answer)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("remote binary %s: %v\n%s", mode, err, output)
			}
			if indexRequests.Load() != 1 {
				t.Errorf("Packages fetched %d times, want once", indexRequests.Load())
			}
			if mode == "execute" {
				if archiveRequests.Load() != 2 {
					t.Fatalf("acquired %d archives, want dependency and target", archiveRequests.Load())
				}
				for _, name := range []string{"demo", "dependency"} {
					if data, err := os.ReadFile(filepath.Join(root, "usr/share/audit", name)); err != nil || string(data) != name {
						t.Fatalf("binary %s payload = %q, %v", name, data, err)
					}
				}
			} else {
				if archiveRequests.Load() != 0 {
					t.Errorf("planning acquired %d archives", archiveRequests.Load())
				}
				for _, path := range []string{"packages", "world", "resume", "usr"} {
					if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
						t.Errorf("planning created %s: %v", path, err)
					}
				}
			}
		})
	}
}
