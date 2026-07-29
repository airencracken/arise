package vendorartifact

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":                    "module github.com/airencracken/arise\n",
		"go.sum":                    "sum\n",
		"vendor/modules.txt":        "# example.org/a v1.2.3\n## explicit\n",
		"vendor/example.org/a/a.go": "package a\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCreateEncodeDecodeVerify(t *testing.T) {
	root := fixture(t)
	manifest, err := Create(root, "1.2.3", strings.Repeat("a", 40), "go1.25", 123)
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := Encode(&encoded, manifest); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, decoded); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsMutation(t *testing.T) {
	root := fixture(t)
	manifest, err := Create(root, "1.2.3", "commit", "go1.25", 123)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor/example.org/a/a.go"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, manifest); err == nil {
		t.Fatal("mutation was accepted")
	}
}

func TestVerifyIdentityRejectsWrongRelease(t *testing.T) {
	manifest := Manifest{Version: "1.2.3", SourceCommit: "abc"}
	if err := VerifyIdentity(manifest, "1.2.3", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyIdentity(manifest, "1.2.4", "abc"); err == nil {
		t.Fatal("wrong version was accepted")
	}
	if err := VerifyIdentity(manifest, "1.2.3", "def"); err == nil {
		t.Fatal("wrong commit was accepted")
	}
}

func TestTreeHashIsOrderIndependentAndRejectsSymlinks(t *testing.T) {
	left, right := fixture(t), fixture(t)
	leftHash, err := TreeHash(filepath.Join(left, "vendor"))
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := TreeHash(filepath.Join(right, "vendor"))
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("equal trees differ: %s != %s", leftHash, rightHash)
	}
	if err := os.Symlink("modules.txt", filepath.Join(left, "vendor", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeHash(filepath.Join(left, "vendor")); err == nil {
		t.Fatal("symlink was accepted")
	}
}

func TestDecodeRejectsUnknownAndTrailingData(t *testing.T) {
	for _, input := range []string{
		`{"schema":"x","unknown":true}`,
		`{"schema":"x"} {}`,
	} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted invalid manifest %q", input)
		}
	}
}
