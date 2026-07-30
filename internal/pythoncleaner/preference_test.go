package pythoncleaner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferredPolicyTargetUsesSingleThenOrderedTarget(t *testing.T) {
	target, err := PreferredPolicyTarget(Policy{
		Targets: []string{"python3_14", "python3_13"}, SingleTarget: "python3_13",
	})
	if err != nil || target != "python3_13" {
		t.Fatalf("target = %q, %v", target, err)
	}
	target, err = PreferredPolicyTarget(Policy{Targets: []string{"python3_14"}})
	if err != nil || target != "python3_14" {
		t.Fatalf("target = %q, %v", target, err)
	}
	if _, err := PreferredPolicyTarget(Policy{}); err == nil {
		t.Fatal("empty policy accepted")
	}
}

func TestPublishPreferenceIsAtomicPreservesFallbacksAndRemovesContradiction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "python-exec.conf")
	input := "# local preference\npython3.13\n-python3.14\n\npython3.12\npython3.14\n"
	if err := os.WriteFile(path, []byte(input), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := PublishPreference(path, "python3_14"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "python3.14\n# local preference\npython3.13\n\npython3.12\n"
	if string(data) != want {
		t.Fatalf("preference = %q, want %q", data, want)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v", info.Mode().Perm(), err)
	}
	preference, err := ParsePreference(path)
	if err != nil || len(preference) == 0 || preference[0] != "python3_14" {
		t.Fatalf("published preference = %v, %v", preference, err)
	}
}

func TestPublishPreferenceRejectsSymlinkAndInvalidTarget(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.WriteFile(real, []byte("python3.13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := PublishPreference(link, "python3_14"); err == nil {
		t.Fatal("symlink preference accepted")
	}
	if err := PublishPreference(real, "../../escape"); err == nil {
		t.Fatal("invalid preference target accepted")
	}
}

func TestPublishPreferenceRenameFailurePreservesPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "python-exec.conf")
	if err := os.WriteFile(path, []byte("python3.13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	original := renamePreference
	t.Cleanup(func() { renamePreference = original })
	renamePreference = func(string, string) error { return errors.New("injected rename failure") }
	if err := PublishPreference(path, "python3_14"); err == nil {
		t.Fatal("rename failure ignored")
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "python3.13" {
		t.Fatalf("previous preference changed: %q %v", data, err)
	}
}
