package integration

import (
	"os/exec"
	"testing"
)

func PortageAvailable() bool {
	_, errEmerge := exec.LookPath("emerge")
	_, errPortageq := exec.LookPath("portageq")
	return errEmerge == nil && errPortageq == nil
}

func RequirePortage(t *testing.T) {
	t.Helper()
	if !PortageAvailable() {
		t.Skip("portage tools (emerge, portageq) not available; skipping integration test")
	}
}

func RequireGentoo(t *testing.T) {
	t.Helper()
	// Check for /etc/gentoo-release as the canonical Gentoo marker.
	// This avoids false positives on other Linux distributions that may
	// happen to have portage installed (e.g. via a chroot).
	if exec.Command("test", "-f", "/etc/gentoo-release").Run() != nil {
		t.Skip("not on a Gentoo system; skipping integration test")
	}
}

type Check struct {
	Name    string
	Portage func() (string, error)
	Gm      func() (string, error)
	Want    string
}

func RunCheck(t *testing.T, c Check) {
	t.Helper()
	t.Run(c.Name, func(t *testing.T) {
		t.Helper()

		portageVal, portageErr := c.Portage()
		ariseVal, ariseErr := c.Gm()

		if c.Want != "" {
			if portageErr != nil && ariseErr != nil {
				t.Logf("both errored; portage=%v arise=%v", portageErr, ariseErr)
				return
			}
			if portageErr != nil {
				t.Fatalf("portage error: %v", portageErr)
			}
			if ariseErr != nil {
				t.Fatalf("arise error: %v", ariseErr)
			}
			if portageVal != c.Want || ariseVal != c.Want {
				t.Errorf("want=%q portage=%q arise=%q", c.Want, portageVal, ariseVal)
			}
			return
		}

		if portageErr != nil && ariseErr != nil {
			t.Logf("both errored; portage=%v arise=%v", portageErr, ariseErr)
			return
		}
		if portageErr != nil {
			t.Fatalf("portage error: %v", portageErr)
		}
		if ariseErr != nil {
			t.Fatalf("arise error: %v", ariseErr)
		}

		if portageVal != ariseVal {
			t.Errorf("mismatch:\n  portage: %s\n  arise:      %s", portageVal, ariseVal)
		}
	})
}
