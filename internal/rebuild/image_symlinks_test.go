package rebuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWorkDirSymlinks(t *testing.T) {
	work, image := t.TempDir(), t.TempDir()
	certs := filepath.Join(image, "etc", "ssl", "certs")
	if err := os.MkdirAll(certs, 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		reject bool
	}{
		{work, true}, {work + "//image/usr/share/root.crt", true},
		{"../../.." + work + "/image/usr/share/root.crt", true},
		{work + "-other/image", false}, {"/usr/share/certs/missing", false}, {"../../../usr/share/certs/root.crt", false},
	} {
		link := filepath.Join(certs, "root.pem")
		if err := os.Symlink(tc.target, link); err != nil {
			t.Fatal(err)
		}
		err := checkWorkDirSymlinks(image, work)
		if tc.reject && (err == nil || !strings.Contains(err.Error(), "/etc/ssl/certs/root.pem -> "+tc.target)) {
			t.Fatalf("target %s: %v", tc.target, err)
		}
		if !tc.reject && err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
}
