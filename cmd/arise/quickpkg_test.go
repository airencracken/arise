package main

import (
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestRecoveryArtifactNoticeExposesClassificationSchemaAndDigest(t *testing.T) {
	manifest := &binpkg.RecoveryManifest{
		Schema:       binpkg.RecoveryManifestSchema,
		ArtifactKind: binpkg.ArtifactKindRecovery,
		Package:      binpkg.PackageIdentity{Category: "sys-devel", Package: "fixture", Version: "1"},
	}
	notice, err := recoveryArtifactNotice(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"host-recovery", "schema=1", "manifest-sha256="} {
		if !strings.Contains(notice, required) {
			t.Errorf("notice %q omits %q", notice, required)
		}
	}
	if strings.Contains(notice, "\n") {
		t.Fatalf("notice contains an unexpected newline: %q", notice)
	}
}
