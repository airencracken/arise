package support

import (
	"os"
	"strings"
	"testing"
)

func TestFreshStage3RunbookContract(t *testing.T) {
	data, err := os.ReadFile("../docs/fresh-stage3.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	required := []string{
		"emerge --oneshot dev-vcs/git app-eselect/eselect-repository",
		"emaint sync -r arise-overlay",
		"sys-apps/arise ~amd64",
		"emerge --ask sys-apps/arise",
		"arise sync",
		"--save-plan stage3-world",
		"--approve-plan stage3-world",
		"arise recover status",
		"arise --resume update @world",
		"arise dispatch-conf",
		"arise news list",
		"arise maintain world --check",
		"arise maintain moveinst --check",
		"arise maintain merges --check",
		"arise maintain resume --check",
		"arise info --preserved-libs",
		"--with-bdeps=y",
		"reboot",
	}
	for _, text := range required {
		if !strings.Contains(document, text) {
			t.Errorf("fresh-stage3 runbook is missing %q", text)
		}
	}
}

func TestFreshStage3RunbookPreservesSafetyBoundary(t *testing.T) {
	data, err := os.ReadFile("../docs/fresh-stage3.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, forbidden := range []string{
		"emerge --depclean sys-apps/portage",
		"rm -rf /var/tmp/arise",
		"--jobs-tmpdir-require-free-gb=0",
		"--emptytree",
	} {
		if strings.Contains(document, forbidden) {
			t.Errorf("fresh-stage3 runbook crosses safety boundary with %q", forbidden)
		}
	}
}
