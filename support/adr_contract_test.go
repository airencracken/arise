package support

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var adrNamePattern = regexp.MustCompile(`^([0-9]{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)

func TestArchitectureDecisionRecordsAreIndexedAndWellFormed(t *testing.T) {
	entries, err := os.ReadDir("../docs/adr")
	if err != nil {
		t.Fatal(err)
	}
	indexData, err := os.ReadFile("../docs/adr/README.md")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexData)
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !adrNamePattern.MatchString(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no architecture decision records found")
	}
	requiredSections := []string{
		"## Status\n",
		"## Context\n",
		"## Decision\n",
		"## Consequences\n",
	}
	for position, name := range names {
		number := adrNamePattern.FindStringSubmatch(name)[1]
		wantNumber := fmt.Sprintf("%04d", position+1)
		if number != wantNumber {
			t.Errorf("ADR sequence contains %s, want %s at position %d", number, wantNumber, position)
		}
		data, readErr := os.ReadFile(filepath.Join("../docs/adr", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		document := string(data)
		if !strings.HasPrefix(document, "# ADR-"+number+": ") {
			t.Errorf("%s title does not start with ADR number", name)
		}
		for _, section := range requiredSections {
			if !strings.Contains(document, section) {
				t.Errorf("%s is missing %q", name, strings.TrimSpace(section))
			}
		}
		if !strings.Contains(index, "("+name+")") {
			t.Errorf("%s is not linked from docs/adr/README.md", name)
		}
	}
}

func TestArchitectureDecisionTemplateContainsRequiredLifecycleGuidance(t *testing.T) {
	data, err := os.ReadFile("../docs/adr/template.md")
	if err != nil {
		t.Fatal(err)
	}
	template := string(data)
	for _, required := range []string{
		"## Status",
		"Proposed on YYYY-MM-DD.",
		"## Context",
		"## Decision",
		"## Consequences",
	} {
		if !strings.Contains(template, required) {
			t.Errorf("ADR template is missing %q", required)
		}
	}
}
