package recoveryset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestPublishAtomicallyCreatesCompleteVerifiedSet(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	first := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	second := createInstalledFixture(t, base, root, "app-misc", "second", "2", "usr/bin/second")
	request := validRequest(base, root, []Package{{VDBEntryPath: first}, {VDBEntryPath: second}})

	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.State != "complete" || manifest.SetID != request.SetID || len(manifest.Artifacts) != 2 {
		t.Fatalf("published manifest = %+v", manifest)
	}
	if manifest.Artifacts[0].Identity >= manifest.Artifacts[1].Identity {
		t.Fatalf("artifacts are not canonically ordered: %+v", manifest.Artifacts)
	}
	for _, artifact := range manifest.Artifacts {
		if !strings.HasPrefix(artifact.Path, "artifacts/") {
			t.Fatalf("artifact path %q is outside the set artifact tree", artifact.Path)
		}
	}
	if staging, err := filepath.Glob(filepath.Join(base, "recovery", "sets", ".*.tmp-*")); err != nil || len(staging) != 0 {
		t.Fatalf("publication retained staging directories %v: %v", staging, err)
	}
}

func TestPublishFailureLeavesNoVisibleOrStagedSet(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	first := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	second := createInstalledFixture(t, base, root, "app-misc", "second", "2", "usr/bin/second")
	request := validRequest(base, root, []Package{{VDBEntryPath: first}, {VDBEntryPath: second}})
	calls := 0
	request.Capture = func(ctx context.Context, capture binpkg.CaptureRequest) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("injected capture failure")
		}
		return binpkg.CreateRecoveryArtifact(ctx, capture)
	}
	if path, err := Publish(context.Background(), request); err == nil || path != "" {
		t.Fatalf("Publish() = %q, %v; want injected failure", path, err)
	}
	if _, err := os.Stat(filepath.Join(request.Directory, "sets", request.SetID)); !os.IsNotExist(err) {
		t.Fatalf("failed set became visible: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(request.Directory, "sets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed publication retained entries: %v", entries)
	}
}

func TestPublishFaultInjectionAtEveryCaptureBoundaryIsAtomic(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(fmt.Sprintf("capture-%d", failAt), func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "root")
			var packages []Package
			for index, name := range []string{"first", "second", "third"} {
				vdb := createInstalledFixture(t, base, root, "app-misc", name, fmt.Sprint(index+1), "usr/bin/"+name)
				packages = append(packages, Package{VDBEntryPath: vdb})
			}
			request := validRequest(base, root, packages)
			calls := 0
			request.Capture = func(ctx context.Context, capture binpkg.CaptureRequest) (string, error) {
				calls++
				if calls == failAt {
					return "", errors.New("injected boundary failure")
				}
				return binpkg.CreateRecoveryArtifact(ctx, capture)
			}
			if _, err := Publish(context.Background(), request); err == nil {
				t.Fatal("Publish() accepted injected capture failure")
			}
			if _, err := os.Stat(filepath.Join(request.Directory, "sets", request.SetID)); !os.IsNotExist(err) {
				t.Fatalf("partial set became visible: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(request.Directory, "sets"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("staging entries remain: %v", entries)
			}
		})
	}
}

func TestPublishCancellationLeavesNoSet(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Publish(ctx, request); err == nil {
		t.Fatal("Publish() ignored cancellation")
	}
	if _, err := os.Stat(filepath.Join(request.Directory, "sets", request.SetID)); !os.IsNotExist(err) {
		t.Fatalf("cancelled set became visible: %v", err)
	}
}

func TestReadRejectsArtifactTampering(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(path, filepath.FromSlash(manifest.Artifacts[0].Path))
	if err := os.Chmod(artifact, 0644); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(artifact, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("tamper")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Read() tamper error = %v", err)
	}
}

func TestPublishedManifestRequiresTrustedValidSignature(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Signature = strings.Repeat("A", len(manifest.Signature))
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "manifest.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("Read() signature tamper error = %v", err)
	}
}

func TestPublishedSigningKeyIsPrivateAndTrustAnchorTamperingFailsClosed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(request.Directory, "signing.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("signing key mode = %o", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(request.Directory, "trusted.pub"), []byte("untrusted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("Read() trust-anchor tamper error = %v", err)
	}
}

func TestConcurrentPublicationAllowsOnlyOneCompleteSet(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := Publish(context.Background(), request)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent publication successes = %d, want 1", successes)
	}
	if _, err := Read(filepath.Join(request.Directory, "sets", request.SetID)); err != nil {
		t.Fatalf("winning set is invalid: %v", err)
	}
}

func TestIdenticalArtifactsShareContentAddressedObjectAcrossSets(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	firstPath, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.SetID, request.OperationID = "set-2", "operation-2"
	secondPath, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Read(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Read(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifacts[0].SHA256 != second.Artifacts[0].SHA256 {
		t.Fatalf("identical capture digests = %s and %s", first.Artifacts[0].SHA256, second.Artifacts[0].SHA256)
	}
	firstInfo, err := os.Stat(filepath.Join(firstPath, filepath.FromSlash(first.Artifacts[0].Path)))
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(filepath.Join(secondPath, filepath.FromSlash(second.Artifacts[0].Path)))
	if err != nil {
		t.Fatal(err)
	}
	objectPath := filepath.Join(request.Directory, "objects", "sha256", first.Artifacts[0].SHA256)
	objectInfo, err := os.Stat(objectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) || !os.SameFile(firstInfo, objectInfo) {
		t.Fatal("identical set artifacts do not share their content-addressed object")
	}
	objects, err := os.ReadDir(filepath.Dir(objectPath))
	if err != nil || len(objects) != 1 {
		t.Fatalf("content objects = %v, %v", objects, err)
	}
}

func TestRequestValidationRejectsAdversarialInputs(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	valid := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"set traversal", func(request *Request) { request.SetID = "../outside" }},
		{"operation newline", func(request *Request) { request.OperationID = "bad\nid" }},
		{"plan digest", func(request *Request) { request.PlanSHA256 = "bad" }},
		{"empty packages", func(request *Request) { request.Packages = nil }},
		{"duplicate package", func(request *Request) { request.Packages = append(request.Packages, request.Packages[0]) }},
		{"missing fingerprint", func(request *Request) { request.ConfigurationFingerprint = binpkg.InputFingerprint{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Packages = append([]Package(nil), valid.Packages...)
			test.mutate(&request)
			if _, err := Publish(context.Background(), request); err == nil {
				t.Fatalf("Publish() accepted %s", test.name)
			}
		})
	}
}

func TestManifestSchemaValidation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"future schema", func(value *Manifest) { value.Schema++ }},
		{"incomplete state", func(value *Manifest) { value.State = "staging" }},
		{"plan mismatch", func(value *Manifest) { value.Capture.PlanSHA256 = strings.Repeat("d", 64) }},
		{"set mismatch", func(value *Manifest) { value.Capture.RecoverySetID = "other" }},
		{"empty artifacts", func(value *Manifest) { value.Artifacts = nil }},
		{"bad artifact digest", func(value *Manifest) { value.Artifacts[0].SHA256 = "bad" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := *manifest
			value.Artifacts = append([]Artifact(nil), manifest.Artifacts...)
			test.mutate(&value)
			if err := validateManifest(value, len(value.Artifacts)); err == nil {
				t.Fatalf("validateManifest() accepted %s", test.name)
			}
		})
	}
}

func TestReadRejectsTrailingManifestJSON(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(path, "manifest.json")
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{}"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Read() trailing JSON error = %v", err)
	}
}

func validRequest(base, root string, packages []Package) Request {
	return Request{
		Directory: filepath.Join(base, "recovery"), SetID: "set-1", OperationID: "operation-1",
		PlanSHA256: strings.Repeat("c", 64), RootDir: root, Packages: packages,
		ConfigurationFingerprint: binpkg.InputFingerprint{
			Scope: "portage-configuration", SHA256: strings.Repeat("a", 64), Complete: true,
		},
		RepositoryFingerprint: binpkg.InputFingerprint{
			Scope: "selected-repository-source-closure", SHA256: strings.Repeat("b", 64), Complete: true,
		},
	}
}

func createInstalledFixture(t *testing.T, base, root, category, packageName, version, installedPath string) string {
	t.Helper()
	payload := filepath.Join(root, filepath.FromSlash(installedPath))
	if err := os.MkdirAll(filepath.Dir(payload), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte(category+"/"+packageName), 0755); err != nil {
		t.Fatal(err)
	}
	vdb := filepath.Join(base, "vdb", category, packageName+"-"+version)
	if err := os.MkdirAll(vdb, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"CONTENTS": "obj /" + installedPath + " digest 1700000000\n",
		"CATEGORY": category, "PF": packageName + "-" + version, "SLOT": "0",
		"USE": "", "EAPI": "8", "BUILD_TIME": "1700000000", "repository": "gentoo",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(vdb, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return vdb
}
