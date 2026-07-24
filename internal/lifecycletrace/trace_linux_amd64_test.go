//go:build linux && amd64

package lifecycletrace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/airencracken/arise/internal/journal"
)

func skipRestrictedPtrace(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, syscall.EPERM) || strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("ptrace unavailable in restricted test environment: %v", err)
	}
}

func TestRunPrototypeStopsBeforeChildAndGrandchildMutations(t *testing.T) {
	directory := t.TempDir()
	direct := filepath.Join(directory, "direct")
	grandchild := filepath.Join(directory, "grandchild")
	command := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", "printf direct > \"$1\"; (printf child > \"$2\")", "trace-test", direct, grandchild)
	var observed []string
	err := RunPrototype(command, func(paths []string) error {
		for _, path := range paths {
			if path == direct || path == grandchild {
				observed = append(observed, path)
			}
		}
		return nil
	})
	if err != nil {
		skipRestrictedPtrace(t, err)
		t.Fatalf("trace prototype: %v", err)
	}
	if !slices.Contains(observed, direct) || !slices.Contains(observed, grandchild) {
		t.Fatalf("observed mutations=%v", observed)
	}
}

func TestRunPrototypeCapturesDurablePreimageBeforeOverwrite(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "root")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	operation, err := journal.Begin(filepath.Join(directory, "journals"), root)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", "printf new > \"$1\"", "trace-test", target)
	err = RunPrototype(command, func(paths []string) error {
		for _, path := range paths {
			if path == target {
				return operation.Capture(path)
			}
		}
		return nil
	})
	if err != nil {
		skipRestrictedPtrace(t, err)
		t.Fatalf("trace and capture: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new" {
		t.Fatalf("mutated target=%q err=%v", got, err)
	}
	if err := operation.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "old" {
		t.Fatalf("rolled-back target=%q err=%v", got, err)
	}
}

func TestRunPrototypeCaptureFailurePreventsMutation(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", "printf new > \"$1\"", "trace-test", target)
	injected := errors.New("injected capture failure")
	err := RunPrototype(command, func(paths []string) error {
		if slices.Contains(paths, target) {
			return injected
		}
		return nil
	})
	if err != nil {
		skipRestrictedPtrace(t, err)
	}
	if !errors.Is(err, injected) && !strings.Contains(fmt.Sprint(err), injected.Error()) {
		t.Fatalf("trace error=%v", err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "old" {
		t.Fatalf("capture failure permitted mutation: target=%q err=%v", got, readErr)
	}
}
