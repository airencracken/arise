package support

import (
	"os"
	"strings"
	"testing"
)

func TestRelease0023DocumentsStage3ParityFixes(t *testing.T) {
	data, err := os.ReadFile("../docs/releases/0.0.23.md")
	if err != nil {
		t.Fatal(err)
	}
	release := strings.Join(strings.Fields(string(data)), " ")
	for _, required := range []string{
		"complete shell programs",
		"effective profile policy after IUSE defaults",
		"same 65-package `-uDN @world` plan",
		"six new packages, one upgrade, and 58 reinstalls",
		"passed the previously failing `net-misc/curl` lifecycle preflight",
		"`--verbose` retains the complete artifact stream",
		"without adding ANSI sequences to automatic redirected or JSON output",
		"honors `NO_COLOR`, Portage's `NOCOLOR`, and `TERM=dumb`",
	} {
		if !strings.Contains(release, required) {
			t.Errorf("0.0.23 release notes lost validated contract %q", required)
		}
	}
}
