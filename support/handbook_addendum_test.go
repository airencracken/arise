package support

import (
	"os"
	"strings"
	"testing"
)

func TestHandbookAddendumReferenceConfiguration(t *testing.T) {
	data, err := os.ReadFile("../docs/handbook-addendum.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	required := []string{
		"sync-uri = https://anongit.gentoo.org/git/repo/sync/gentoo.git",
		"sync-uri = https://github.com/airencracken/arise-overlay.git",
		"sync-type = git",
		"clone-depth = 1",
		"sync-depth = 1",
		"emaint sync -r gentoo",
		"emaint sync -r arise-overlay",
		"sys-apps/arise ~amd64",
		"arise sync",
		"--save-plan stage3-world",
		"--approve-plan stage3-world",
		"arise recover status",
		"arise --resume update @world",
		"arise maintain world --check",
		"arise maintain moveinst --check",
		"arise maintain merges --check",
		"arise maintain resume --check",
		"arise info --preserved-libs",
		"G1 maintenance gate",
	}
	for _, requiredText := range required {
		if !strings.Contains(document, requiredText) {
			t.Errorf("Handbook addendum is missing %q", requiredText)
		}
	}
}

func TestHandbookAddendumRejectsUnsafeOrUnsupportedDefaults(t *testing.T) {
	data, err := os.ReadFile("../docs/handbook-addendum.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, forbidden := range []string{
		"sync-type = rsync",
		"ACCEPT_KEYWORDS=\"~amd64\" emerge",
		"COMMON_FLAGS=\"-march=native",
		"emerge --depclean sys-apps/portage",
		"rm -rf /var/tmp/arise",
		"--jobs-tmpdir-require-free-gb=0",
	} {
		if strings.Contains(document, forbidden) {
			t.Errorf("Handbook addendum contains forbidden guidance %q", forbidden)
		}
	}
}
