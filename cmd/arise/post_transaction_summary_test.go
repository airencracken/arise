package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestPrintSavedPackageMessagesSummary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "var", "log", "portage", "elog", "summary.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("LOG: remember this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	printSavedPackageMessagesSummary(&output, root, nil)
	if got := output.String(); !strings.Contains(got, "/var/log/portage/elog/summary.log") || !strings.Contains(got, "less ") {
		t.Fatalf("saved-message reminder = %q", got)
	}
}

func TestPendingConfigUpdatesRecursesDeduplicatesAndHonorsMask(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"etc/._cfg0000_locale.gen",
		"etc/ssh/._cfg0002_sshd_config",
		"etc/masked/._cfg0000_hidden",
		"etc/not-a-config-update",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("new"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &portage.Config{MakeConf: map[string]string{
		"CONFIG_PROTECT":      "/etc /etc/ssh",
		"CONFIG_PROTECT_MASK": "/etc/masked",
	}}
	got, err := pendingConfigUpdates(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/etc/._cfg0000_locale.gen", "/etc/ssh/._cfg0002_sshd_config"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pending=%v want %v", got, want)
	}
}
