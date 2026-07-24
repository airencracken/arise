//go:build live_portage

package rebuild

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledPortageQACheckDiscovery(t *testing.T) {
	if _, err := os.Stat("/usr/lib/portage"); os.IsNotExist(err) {
		t.Skip("installed Portage QA checks unavailable")
	}
	checks, err := installQAChecks("/var/db/repos/gentoo")
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool)
	for _, check := range checks {
		found[filepath.Base(check)] = true
	}
	for _, name := range []string{"60systemd", "60udev", "80libraries", "80multilib-strict"} {
		if !found[name] {
			t.Errorf("installed Portage QA check %s was not discovered; checks=%v", name, checks)
		}
	}
}
