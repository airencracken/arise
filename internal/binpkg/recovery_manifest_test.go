package binpkg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRecoveryManifestRoundTripIncludesCompleteVDBAndPayloadEvidence(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /usr/bin/item digest 1700000000\ndir /usr/bin\n")
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "item"), []byte("payload"), 0751); err != nil {
		t.Fatal(err)
	}
	environment := bytes.Repeat([]byte("environment-data-"), 512)
	if err := os.WriteFile(filepath.Join(vdb, "environment.bz2"), environment, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vdb, "repository"), []byte("gentoo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"BUILD_ID": "42\n", "ABI": "amd64\n", "CBUILD": "x86_64-pc-linux-gnu\n",
		"CHOST": "x86_64-pc-linux-gnu\n", "CTARGET": "x86_64-pc-linux-gnu\n",
	} {
		if err := os.WriteFile(filepath.Join(vdb, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	path, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadRecoveryManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != RecoveryManifestSchema || manifest.ArtifactKind != ArtifactKindRecovery {
		t.Fatalf("manifest contract = schema %d kind %q", manifest.Schema, manifest.ArtifactKind)
	}
	if manifest.Package.Category != "sys-devel" || manifest.Package.Package != "fixture" ||
		manifest.Package.Version != "1.0" || manifest.Package.Repository != "gentoo" ||
		manifest.Package.BuildID != "42" || manifest.Package.ABI != "amd64" ||
		manifest.Package.CBuild != "x86_64-pc-linux-gnu" ||
		manifest.Package.CHOST != "x86_64-pc-linux-gnu" ||
		manifest.Package.CTarget != "x86_64-pc-linux-gnu" {
		t.Fatalf("package identity = %+v", manifest.Package)
	}
	if len(manifest.SourceVDB) < 8 {
		t.Fatalf("source VDB evidence has only %d entries", len(manifest.SourceVDB))
	}
	var foundEnvironment bool
	for _, item := range manifest.SourceVDB {
		if item.Path != "environment.bz2" {
			continue
		}
		foundEnvironment = true
		content, err := base64.StdEncoding.DecodeString(item.ContentBase64)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, environment) {
			t.Fatal("manifest did not preserve the complete VDB environment")
		}
	}
	if !foundEnvironment {
		t.Fatal("manifest omitted environment.bz2")
	}
	if len(manifest.Payload) != 2 || manifest.Payload[0].Path != "usr/bin" ||
		manifest.Payload[1].Path != "usr/bin/item" || manifest.Payload[1].SHA256 == "" {
		t.Fatalf("payload evidence = %+v", manifest.Payload)
	}
	if len(manifest.SourceVDBSHA256) != 64 || len(manifest.SourceRootSHA256) != 64 {
		t.Fatalf("evidence digests = VDB %q root %q", manifest.SourceVDBSHA256, manifest.SourceRootSHA256)
	}
}

func TestRecoveryManifestIsDeterministicForUnchangedState(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /usr/bin/item digest 1700000000\n")
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "item"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	firstPath, err := Create(context.Background(), vdb, root, filepath.Join(base, "first"))
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := Create(context.Background(), vdb, root, filepath.Join(base, "second"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := ReadRecoveryManifest(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadRecoveryManifest(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("unchanged state produced manifest digests %s and %s", firstDigest, secondDigest)
	}
}

func TestReadRecoveryManifestRejectsTampering(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /usr/bin/item digest 1700000000\n")
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "item"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	path, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, start, ok := xpakValueRangeForTest(data, recoveryManifestKey)
	if !ok || len(value) == 0 {
		t.Fatal("artifact omits manifest metadata")
	}
	data[start] = differentBase64Byte(data[start])
	tampered := filepath.Join(base, "tampered.tbz2")
	if err := os.WriteFile(tampered, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecoveryManifest(tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("ReadRecoveryManifest() tamper error = %v", err)
	}
}

func TestReadRecoveryManifestRejectsMetadataIdentityMismatch(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "")
	path, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, start, ok := xpakValueRangeForTest(data, "CATEGORY")
	if !ok || string(value) != "sys-devel\n" {
		t.Fatal("artifact omits CATEGORY")
	}
	copy(data[start:start+len(value)], []byte("app-devel\n"))
	tampered := filepath.Join(base, "identity-mismatch.tbz2")
	if err := os.WriteFile(tampered, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecoveryManifest(tampered); err == nil || !strings.Contains(err.Error(), "identity disagrees") {
		t.Fatalf("ReadRecoveryManifest() identity error = %v", err)
	}
}

func xpakValueRangeForTest(packageData []byte, key string) ([]byte, int, bool) {
	if len(packageData) < 16 {
		return nil, 0, false
	}
	segmentSize := int(binary.BigEndian.Uint32(packageData[len(packageData)-8 : len(packageData)-4]))
	start := len(packageData) - segmentSize - 8
	if start < 0 {
		return nil, 0, false
	}
	segment := packageData[start : start+segmentSize+8]
	value, ok := xpakValueForTest(segment, key)
	if !ok {
		return nil, 0, false
	}
	for offset := 16 + int(binary.BigEndian.Uint32(segment[8:12])); offset <= len(segment)-len(value); offset++ {
		if bytes.Equal(segment[offset:offset+len(value)], value) {
			return value, start + offset, true
		}
	}
	return nil, 0, false
}

func TestRecoveryManifestSchemaValidation(t *testing.T) {
	base := validRecoveryManifestForTest()
	tests := []struct {
		name   string
		mutate func(*RecoveryManifest)
	}{
		{"future schema", func(manifest *RecoveryManifest) { manifest.Schema++ }},
		{"wrong kind", func(manifest *RecoveryManifest) { manifest.ArtifactKind = "repository-binpkg" }},
		{"unsafe category", func(manifest *RecoveryManifest) { manifest.Package.Category = "../escape" }},
		{"payload digest mismatch", func(manifest *RecoveryManifest) { manifest.SourceRootSHA256 = strings.Repeat("0", 64) }},
		{"duplicate payload", func(manifest *RecoveryManifest) {
			manifest.Payload = append(manifest.Payload, manifest.Payload[0])
			digest, _ := evidenceSHA256(manifest.Payload)
			manifest.SourceRootSHA256 = digest
		}},
		{"unknown evidence type", func(manifest *RecoveryManifest) {
			manifest.Payload[0].Type = "device"
			digest, _ := evidenceSHA256(manifest.Payload)
			manifest.SourceRootSHA256 = digest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := base
			manifest.SourceVDB = append([]FileEvidence(nil), base.SourceVDB...)
			manifest.Payload = append([]FileEvidence(nil), base.Payload...)
			test.mutate(&manifest)
			if err := validateRecoveryManifest(&manifest); err == nil {
				t.Fatalf("validateRecoveryManifest() accepted %s", test.name)
			}
		})
	}
}

func TestRecoveryManifestRejectsSpecialVDBFiles(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "")
	fifo := filepath.Join(vdb, "host-fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages")); err == nil {
		t.Fatal("Create() accepted a special file in the source VDB")
	}
}

func validRecoveryManifestForTest() RecoveryManifest {
	content := []byte("8")
	contentSum := sha256.Sum256(content)
	vdb := []FileEvidence{{Path: "EAPI", Type: "file", Mode: 0644, Size: 1, SHA256: hex.EncodeToString(contentSum[:]), ContentBase64: base64.StdEncoding.EncodeToString(content)}}
	payload := []FileEvidence{{Path: "usr/bin/item", Type: "file", Mode: 0755, Size: 1, SHA256: strings.Repeat("b", 64), RecordedType: "obj"}}
	vdbDigest, _ := evidenceSHA256(vdb)
	payloadDigest, _ := evidenceSHA256(payload)
	return RecoveryManifest{
		Schema: RecoveryManifestSchema, ArtifactKind: ArtifactKindRecovery,
		Package:   PackageIdentity{Category: "sys-devel", Package: "fixture", Version: "1"},
		Capture:   LegacyCaptureProvenance(),
		SourceVDB: vdb, Payload: payload, SourceVDBSHA256: vdbDigest, SourceRootSHA256: payloadDigest,
	}
}

func differentBase64Byte(value byte) byte {
	if value == 'A' {
		return 'B'
	}
	return 'A'
}
