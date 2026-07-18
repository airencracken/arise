//go:build live_portage

package main

import (
	"flag"
	"os/exec"
	"strings"
	"testing"
)

// TestLiveEmergeAdvertisesClaimedCLICompatibility guards the spellings that
// Arise intentionally shares with emerge. Behavioral parity belongs in the
// plan differential corpus; this test catches interface drift on the live
// Portage version without claiming that every emerge option is implemented.
func TestLiveEmergeAdvertisesClaimedCLICompatibility(t *testing.T) {
	path, err := exec.LookPath("emerge")
	if err != nil {
		t.Skip("emerge is not installed")
	}
	output, err := exec.Command(path, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("emerge --help: %v: %s", err, output)
	}
	help := string(output)

	for _, option := range []string{
		"--color", "--complete-graph", "--deep", "--jobs", "--keep-going",
		"--load-average", "--newuse", "--oneshot", "--onlydeps", "--reinstall",
		"--with-bdeps",
	} {
		if flag.Lookup(strings.TrimPrefix(option, "--")) == nil {
			t.Errorf("Arise compatibility inventory references unregistered %s", option)
		}
		if !strings.Contains(help, option) {
			t.Errorf("installed emerge no longer advertises claimed option %s", option)
		}
	}

	shortLine := ""
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "Options: -[") {
			shortLine = line
			break
		}
	}
	if shortLine == "" {
		t.Fatal("could not find emerge short-option inventory")
	}
	for _, option := range []string{"u", "O", "o", "e", "N", "D", "p", "a", "q", "v", "t", "b", "B", "k", "K", "f", "n", "g", "G", "j", "l"} {
		if flag.Lookup(option) == nil {
			t.Errorf("Arise does not register claimed emerge option -%s", option)
		}
		if !strings.Contains(shortLine, option) {
			t.Errorf("installed emerge no longer advertises claimed option -%s", option)
		}
	}
}
