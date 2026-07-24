package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteTrapDiagnosticsIncludesRuntimeAndJournalState(t *testing.T) {
	originalJournalDir := *journalDir
	*journalDir = t.TempDir()
	defer func() { *journalDir = originalJournalDir }()
	var output bytes.Buffer
	when := time.Date(2026, 7, 21, 22, 30, 0, 0, time.UTC)
	if err := writeTrapDiagnostics(&output, when); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Arise SIGTRAP diagnostic", "2026-07-21T22:30:00Z", "PID:",
		"Invocation:", "Active journals: none", "Goroutine stacks:",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("diagnostic missing %q:\n%s", expected, output.String())
		}
	}
}
