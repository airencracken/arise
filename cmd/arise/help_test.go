package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEveryRoutableSubcommandHasHelp(t *testing.T) {
	if len(commandOrder) != len(commandHelp) {
		t.Fatalf("command order has %d entries, help registry has %d", len(commandOrder), len(commandHelp))
	}
	seen := make(map[string]bool, len(commandOrder))
	for _, command := range commandOrder {
		if seen[command] {
			t.Fatalf("duplicate command %q", command)
		}
		seen[command] = true
		selected, operands := selectCommand([]string{command, "--help"})
		if selected != command || len(operands) != 1 || operands[0] != "--help" {
			t.Fatalf("%s route = %q %v", command, selected, operands)
		}
		var output bytes.Buffer
		if !writeCommandHelp(&output, command) {
			t.Fatalf("%s has no help", command)
		}
		if !strings.HasPrefix(output.String(), "Usage: arise "+command) {
			t.Fatalf("%s help = %q", command, output.String())
		}
	}
}

func TestHelpRequestContractIsExactAndAdversarial(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		if !isHelpRequest(args) {
			t.Errorf("%v was not recognized", args)
		}
	}
	for _, args := range [][]string{nil, {"--help", "extra"}, {"--HELP"}, {""}} {
		if isHelpRequest(args) {
			t.Errorf("%v was accepted", args)
		}
	}
	if writeCommandHelp(&bytes.Buffer{}, "not-a-command") {
		t.Fatal("unknown command has help")
	}
}

func TestExternalToolNamesAreNotAriseRoutes(t *testing.T) {
	for _, command := range []string{"equery", "portageq", "qlist", "q", "eix", "emerge"} {
		if knownCommand(command) {
			t.Errorf("external tool name %q remains an Arise route", command)
		}
	}
}
