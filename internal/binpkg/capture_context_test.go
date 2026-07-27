package binpkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationFingerprintIsDeterministicAndDetectsDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "package.use"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "make.conf"), []byte("USE=\"ssl\"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.use", "local"), []byte("cat/pkg feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := FingerprintConfiguration(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FingerprintConfiguration(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !first.Complete || first.Scope != "portage-configuration" {
		t.Fatalf("configuration fingerprints = %+v and %+v", first, second)
	}
	if err := os.WriteFile(filepath.Join(root, "package.use", "local"), []byte("cat/pkg -feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := FingerprintConfiguration(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SHA256 == first.SHA256 {
		t.Fatal("configuration content drift did not change its fingerprint")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ssl") || strings.Contains(string(encoded), "cat/pkg") {
		t.Fatalf("configuration fingerprint embeds configuration contents: %s", encoded)
	}
}

func TestRepositoryIdentityFingerprintScopesStandaloneCapture(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"profiles/repo_name":   "gentoo\n",
		"metadata/layout.conf": "masters =\n",
		".git/HEAD":            "ref: refs/heads/main\n",
		".git/refs/heads/main": strings.Repeat("a", 40) + "\n",
		"cat/pkg/pkg-1.ebuild": "EAPI=8\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := FingerprintRepositoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.UnavailableReason == "" || first.Scope != "repository-identity" {
		t.Fatalf("repository identity scope = %+v", first)
	}
	if err := os.WriteFile(filepath.Join(root, "cat/pkg/pkg-1.ebuild"), []byte("EAPI=9\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sourceChanged, err := FingerprintRepositoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if sourceChanged.SHA256 != first.SHA256 {
		t.Fatal("standalone identity fingerprint unexpectedly claimed selected-source coverage")
	}
	if err := os.WriteFile(filepath.Join(root, ".git/refs/heads/main"), []byte(strings.Repeat("b", 40)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	identityChanged, err := FingerprintRepositoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if identityChanged.SHA256 == first.SHA256 {
		t.Fatal("repository commit identity drift did not change its fingerprint")
	}
}

func TestCreateRecoveryArtifactRoundTripsOperationProvenance(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /usr/bin/item digest 1700000000\n")
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "item"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := sha256.Sum256([]byte("approved plan"))
	request := CaptureRequest{
		VDBEntryPath: vdb, RootDir: root, PackageDir: filepath.Join(base, "packages"),
		Provenance: CaptureProvenance{
			Schema:        CaptureContextSchema,
			OperationKind: "pre-update-recovery",
			OperationID:   "operation-17",
			RecoverySetID: "set-4",
			PlanSHA256:    hex.EncodeToString(plan[:]),
			ConfigurationFingerprint: InputFingerprint{
				Scope: "portage-configuration", SHA256: strings.Repeat("a", 64), Complete: true,
			},
			RepositoryFingerprint: InputFingerprint{
				Scope: "selected-source-closure", SHA256: strings.Repeat("b", 64), Complete: true,
			},
		},
	}
	path, err := CreateRecoveryArtifact(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadRecoveryManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Capture != request.Provenance {
		t.Fatalf("capture provenance = %+v, want %+v", manifest.Capture, request.Provenance)
	}
}

func TestCaptureProvenanceSchemaValidation(t *testing.T) {
	valid := CaptureProvenance{
		Schema:        CaptureContextSchema,
		OperationKind: "pre-update-recovery",
		ConfigurationFingerprint: InputFingerprint{
			Scope: "configuration", SHA256: strings.Repeat("a", 64), Complete: true,
		},
		RepositoryFingerprint: InputFingerprint{
			Scope: "repository", SHA256: strings.Repeat("b", 64), Complete: false, UnavailableReason: "identity only",
		},
	}
	tests := []struct {
		name   string
		mutate func(*CaptureProvenance)
	}{
		{"schema", func(value *CaptureProvenance) { value.Schema++ }},
		{"operation kind", func(value *CaptureProvenance) { value.OperationKind = "bad\nkind" }},
		{"operation ID", func(value *CaptureProvenance) { value.OperationID = "bad\x00id" }},
		{"plan digest", func(value *CaptureProvenance) { value.PlanSHA256 = "not-a-digest" }},
		{"configuration scope", func(value *CaptureProvenance) { value.ConfigurationFingerprint.Scope = "" }},
		{"configuration digest", func(value *CaptureProvenance) { value.ConfigurationFingerprint.SHA256 = "bad" }},
		{"partial explanation", func(value *CaptureProvenance) { value.RepositoryFingerprint.UnavailableReason = "" }},
		{"complete contradiction", func(value *CaptureProvenance) {
			value.ConfigurationFingerprint.UnavailableReason = "unavailable"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := validateCaptureProvenance(value); err == nil {
				t.Fatalf("validateCaptureProvenance() accepted invalid %s", test.name)
			}
		})
	}
}

func TestFingerprintPathsRejectsTraversal(t *testing.T) {
	if _, err := fingerprintPaths(t.TempDir(), []string{"../../outside"}); err == nil {
		t.Fatal("fingerprintPaths() accepted traversal")
	}
}
