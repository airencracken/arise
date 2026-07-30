//go:build live_portage

package perlcleaner

import (
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
)

func TestLivePerlCleanerModulesDifferential(t *testing.T) {
	if _, err := exec.LookPath("perl-cleaner"); err != nil {
		t.Skip("perl-cleaner is not installed")
	}
	report, err := Check("/var/db/pkg", ModulesMode())
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("perl-cleaner", "--modules", "--pretend", "--verbose", "--verbose")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("perl-cleaner: %v\n%s", err, output)
	}
	pattern := regexp.MustCompile(`Adding to list:\s+(\S+)`)
	var reference []string
	for _, match := range pattern.FindAllStringSubmatch(string(output), -1) {
		parsed, parseErr := atom.Parse(match[1])
		if parseErr != nil {
			t.Fatalf("parse perl-cleaner atom %q: %v", match[1], parseErr)
		}
		reference = append(reference, parsed.CP())
	}
	var arise []string
	for _, action := range report.Actions {
		parsed, parseErr := atom.Parse(action.Atom)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		arise = append(arise, parsed.CP())
	}
	sort.Strings(reference)
	sort.Strings(arise)
	reference = compact(reference)
	arise = compact(arise)
	if strings.Join(arise, "\n") != strings.Join(reference, "\n") {
		t.Fatalf("module rebuild set differs\nArise: %v\nperl-cleaner: %v\n%s", arise, reference, output)
	}
}

func compact(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
