package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestActiveGuidesUseUpdateFlagInsteadOfRemovedCommand(t *testing.T) {
	obsolete := regexp.MustCompile(`(?m)^arise[^\n]*\supdate(?:\s|$)`)
	for _, path := range []string{"../../README.md", "../../docs/fresh-stage3.md", "../../docs/handbook-addendum.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), "\\\n", "")
		if obsolete.MatchString(text) {
			t.Errorf("%s invokes removed update command", path)
		}
		if !strings.Contains(text, "--update") {
			t.Errorf("%s lacks the supported update flag", path)
		}
	}
	if command, args := selectCommand([]string{"@world"}); command != "install" || len(args) != 1 || args[0] != "@world" {
		t.Fatalf("documented world update route changed: %s %v", command, args)
	}
}
