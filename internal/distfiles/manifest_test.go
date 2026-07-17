package distfiles

import (
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestAndVerify(t *testing.T) {
	content := []byte("verified artifact")
	digest := sha512.Sum512(content)
	manifest := "DIST source.tar " + "17 SHA512 " + hex.EncodeToString(digest[:]) + "\n"
	artifacts, err := ParseManifest(strings.NewReader(manifest))
	if err != nil {
		t.Fatal(err)
	}
	artifact := artifacts["source.tar"]
	path := filepath.Join(t.TempDir(), artifact.Name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, artifact); err != nil {
		t.Fatal(err)
	}
	set := VerifiedSet{Directory: filepath.Dir(path), Artifacts: []Artifact{artifact}}
	if got := set.Paths(); len(got) != 1 || got[0] != path {
		t.Fatalf("paths = %#v", got)
	}
}

func TestVerifyRejectsSizeDigestAndUnsupportedAlgorithms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.tar")
	if err := os.WriteFile(path, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []Artifact{
		{Name: "source.tar", Size: 4, Digests: map[string]string{"SHA512": strings.Repeat("0", 128)}},
		{Name: "source.tar", Size: 3, Digests: map[string]string{"SHA512": strings.Repeat("0", 128)}},
		{Name: "source.tar", Size: 3, Digests: map[string]string{"WHIRLPOOL": strings.Repeat("0", 128)}},
	} {
		if err := Verify(path, artifact); err == nil {
			t.Fatalf("accepted invalid artifact %#v", artifact)
		}
	}
}

func TestVerifyRejectsSymlinkAndUnsafeArtifactName(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "source.tar")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	artifact := Artifact{Name: "source.tar", Size: 1, Digests: map[string]string{"SHA512": strings.Repeat("0", 128)}}
	if err := Verify(link, artifact); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink error = %v", err)
	}
	artifact.Name = "../target"
	if err := Verify(target, artifact); err == nil || !strings.Contains(err.Error(), "unsafe artifact name") {
		t.Fatalf("unsafe-name error = %v", err)
	}
}

func TestParseManifestRejectsUnsafeAndConflictingRecords(t *testing.T) {
	inputs := []string{
		"DIST ../escape 1 SHA512 00\n",
		"DIST source 1 SHA512 00\nDIST source 2 SHA512 00\n",
		"DIST source nope SHA512 00\n",
		"DIST source 1 SHA512 not-hex\n",
	}
	for _, input := range inputs {
		if _, err := ParseManifest(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted manifest %q", input)
		}
	}
}

func TestPlanHonorsUseRenameAndOrderedFallbacks(t *testing.T) {
	manifest := strings.Join([]string{
		"DIST base.tar 1 SHA512 00",
		"DIST renamed.tar 2 SHA512 00",
		"DIST docs.tar 3 SHA512 00",
	}, "\n")
	srcURI := "https://one/base.tar https://two/base.tar doc? ( https://one/docs.tar ) https://one/source.tar -> renamed.tar"
	plan, err := Plan(strings.NewReader(manifest), srcURI, map[string]bool{"doc": false})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].Name != "base.tar" || len(plan[0].Sources) != 2 || plan[1].Name != "renamed.tar" || plan[1].Sources[0] != "https://one/source.tar" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanRejectsSelectedFileMissingFromManifest(t *testing.T) {
	if _, err := Plan(strings.NewReader(""), "https://one/source.tar", nil); err == nil {
		t.Fatal("selected source without Manifest identity accepted")
	}
}
