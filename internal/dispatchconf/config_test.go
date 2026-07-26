package dispatchconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch-conf.conf")
	content := strings.Join([]string{
		"archive-dir=${EPREFIX}/etc/config-archive",
		`diff="diff -Nu '%s' '%s'"`,
		`merge="sdiff --output='%s' '%s' '%s'"`,
		"replace-cvs=yes",
		"replace-wscomments=no",
		"replace-unmodified=yes",
		"ignore-previously-merged=yes",
		`frozen-files="/etc/frozen /etc/other"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path, "/gentoo", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveDir != "/gentoo/etc/config-archive" || !got.ReplaceCVS ||
		got.ReplaceWSComments || !got.ReplaceUnmodified || !got.IgnorePreviouslyMerged ||
		len(got.FrozenFiles) != 2 {
		t.Fatalf("LoadConfig() = %#v", got)
	}
}

func TestLoadConfigRequiresCompatibilityKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch-conf.conf")
	if err := os.WriteFile(path, []byte("archive-dir=/archive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, "", DefaultOptions()); err == nil {
		t.Fatal("incomplete configuration accepted")
	}
}

func TestMutationYesDoesNotAcceptNearMiss(t *testing.T) {
	for _, value := range []string{"y", "true", "yes-no", "1"} {
		if yes(value) {
			t.Fatalf("yes(%q) = true", value)
		}
	}
}
