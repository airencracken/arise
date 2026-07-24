package phaseproto

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeliverElogFiltersClassesAndWritesLocalSinks(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	events := []Event{{Kind: "elog", Class: "INFO", Message: "information"}, {Kind: "elog", Class: "WARN", Message: "warning"}, {Kind: "log", Message: "ordinary"}}
	paths, err := DeliverElog(events, ElogOptions{LogDir: root, Category: "cat", PF: "pkg-1", Classes: []string{"WARN"}, Sinks: []string{"echo", "save", "save-summary"}, Output: &output, Now: time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || !strings.Contains(output.String(), "warning") || strings.Contains(output.String(), "information") {
		t.Fatalf("paths=%v output=%q", paths, output.String())
	}
	saved, err := os.ReadFile(filepath.Join(root, "elog", "cat:pkg-1:20260718-010203.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "WARN: warning") || strings.Contains(string(saved), "information") {
		t.Fatalf("saved elog = %s", saved)
	}
	summary, err := os.ReadFile(filepath.Join(root, "elog", "summary.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "cat/pkg-1") || !strings.Contains(string(summary), "warning") {
		t.Fatalf("summary = %s", summary)
	}
}

func TestValidateElogSinksRejectsUnsupportedBeforeMessages(t *testing.T) {
	for _, sink := range []string{"mail", "mail-summary", "custom", "unknown"} {
		if err := ValidateElogSinks([]string{sink}); err == nil || !strings.Contains(err.Error(), sink) {
			t.Fatalf("sink %q error = %v", sink, err)
		}
	}
	if _, err := DeliverElog(nil, ElogOptions{Sinks: []string{"mail"}}); err == nil {
		t.Fatal("unsupported sink accepted when no messages existed")
	}
}

func TestDeliverElogFailsClosedOnSavePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := DeliverElog([]Event{{Kind: "elog", Class: "ERROR", Message: "failure"}}, ElogOptions{LogDir: root, Category: "cat", PF: "pkg-1", Sinks: []string{"save"}})
	if err == nil {
		t.Fatal("elog save boundary failure was ignored")
	}
}

func TestDeliverElogAcceptsPortageSaveSummarySpelling(t *testing.T) {
	root := t.TempDir()
	_, err := DeliverElog([]Event{{Kind: "elog", Class: "LOG", Message: "read me later"}}, ElogOptions{LogDir: root, Category: "cat", PF: "pkg-1", Sinks: []string{"save_summary:log,warn,error,qa"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "elog", "summary.log"))
	if err != nil || !strings.Contains(string(data), "read me later") {
		t.Fatalf("Portage save_summary output=%q error=%v", data, err)
	}
}
