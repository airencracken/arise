package main

import (
	"testing"

	"github.com/airencracken/arise/internal/metadata"
)

func TestRepositoryMetadataContractCoversPortageKeys(t *testing.T) {
	record := &metadata.PackageMetadata{
		BDEPEND: "build", DEFINED_PHASES: "install", DEPEND: "depend",
		DESCRIPTION: "description", EAPI: "8", HOMEPAGE: "https://example.invalid",
		IDEPEND: "install-depend", INHERITED: "toolchain-funcs", IUSE: "feature",
		KEYWORDS: "amd64", LICENSE: "MIT", PDEPEND: "post", PROPERTIES: "live",
		RDEPEND: "runtime", REQUIRED_USE: "feature", RESTRICT: "test", SLOT: "0",
		SRC_URI: "https://example.invalid/source",
	}
	keys := []string{
		"BDEPEND", "DEFINED_PHASES", "DEPEND", "DESCRIPTION", "EAPI", "HOMEPAGE",
		"IDEPEND", "INHERIT", "INHERITED", "IUSE", "KEYWORDS", "LICENSE",
		"PDEPEND", "PROPERTIES", "RDEPEND", "REQUIRED_USE", "RESTRICT", "SLOT", "SRC_URI",
	}
	for _, key := range keys {
		if value, ok := repositoryMetadataValue(record, key); !ok || value == "" {
			t.Errorf("metadata key %s = %q, supported=%t", key, value, ok)
		}
	}
	if _, ok := repositoryMetadataValue(record, "UNSUPPORTED"); ok {
		t.Fatal("unsupported metadata key accepted")
	}
}

func TestSplitMetadataKeysAcceptsInteroperableSeparators(t *testing.T) {
	got := splitMetadataKeys("EAPI,SLOT KEYWORDS\tLICENSE")
	want := []string{"EAPI", "SLOT", "KEYWORDS", "LICENSE"}
	if len(got) != len(want) {
		t.Fatalf("split keys = %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("split keys = %v, want %v", got, want)
		}
	}
}
