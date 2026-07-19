package phaseproto

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPackageLogOrdinaryLayoutAndOrderedRecords(t *testing.T) {
	root, temp := filepath.Join(t.TempDir(), "logs"), filepath.Join(t.TempDir(), "T")
	manager, err := NewPackageLog(PackageLogOptions{Root: root, TempDir: temp, Category: "dev-lang", PF: "python-3.14", Now: time.Date(2026, 7, 18, 12, 34, 56, 0, time.FixedZone("local", -7*60*60))})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, "dev-lang:python-3.14:20260718-193456.log")
	if manager.Path() != wantPath {
		t.Fatalf("path = %q, want %q", manager.Path(), wantPath)
	}
	if err := manager.WriteRecord(1, "job-1", "src_compile", "log", "stdout", "first"); err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteRecord(2, "job-1", "src_compile", "qa", "stderr", "second"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Finalize(false); err != nil {
		t.Fatal(err)
	}
	canonical, err := os.Readlink(filepath.Join(temp, "build.log"))
	if err != nil || canonical != wantPath {
		t.Fatalf("canonical=%q err=%v", canonical, err)
	}
	content, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "sequence=1") || !strings.Contains(string(content), "sequence=2") || strings.Index(string(content), "sequence=1") > strings.Index(string(content), "sequence=2") {
		t.Fatalf("records = %s", content)
	}
}

func TestPersistWorkerEventsIncludesTerminalFailureAndDurablePath(t *testing.T) {
	manager, err := NewPackageLog(PackageLogOptions{Root: t.TempDir(), TempDir: t.TempDir(), Category: "cat", PF: "pkg-1", Now: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{{Sequence: 1, Kind: "phase", Message: "start"}, {Sequence: 2, Kind: "log", Stream: "stderr", Message: "failed output"}}
	_, err = persistWorkerEvents(Request{ID: "job-1", Phase: "src_compile"}, events, errors.New("worker failed"), WorkerOptions{DurableLog: manager, FinalizeLog: true})
	if err == nil || !strings.Contains(err.Error(), manager.Path()) {
		t.Fatalf("error = %v, want durable path", err)
	}
	content, readErr := os.ReadFile(manager.Path())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "failed output") || !strings.Contains(string(content), "terminal-error") {
		t.Fatalf("failure log = %s", content)
	}
}

func TestParallelPackageLogsNeverInterleave(t *testing.T) {
	root := t.TempDir()
	var wait sync.WaitGroup
	errorsOut := make(chan error, 2)
	for index := 1; index <= 2; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			pf := fmt.Sprintf("pkg-%d", index)
			manager, err := NewPackageLog(PackageLogOptions{Root: root, TempDir: filepath.Join(t.TempDir(), "T"), Category: "cat", PF: pf, Now: time.Unix(int64(index), 0)})
			if err != nil {
				errorsOut <- err
				return
			}
			var events []Event
			for sequence := 1; sequence <= 100; sequence++ {
				events = append(events, Event{Sequence: uint64(sequence), Kind: "log", Stream: "stdout", Message: pf})
			}
			_, err = persistWorkerEvents(Request{ID: pf, Phase: "src_compile"}, events, nil, WorkerOptions{DurableLog: manager, FinalizeLog: true})
			if err == nil {
				content, readErr := os.ReadFile(manager.Path())
				err = readErr
				if err == nil && strings.Contains(string(content), fmt.Sprintf("pkg-%d", 3-index)) {
					err = fmt.Errorf("cross-package interleaving in %s", manager.Path())
				}
			}
			if err != nil {
				errorsOut <- err
			}
		}()
	}
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Error(err)
	}
}

func TestPackageLogSplitCompressionUpdatesCanonicalPath(t *testing.T) {
	base, temp := t.TempDir(), filepath.Join(t.TempDir(), "T")
	manager, err := NewPackageLog(PackageLogOptions{Root: base, TempDir: temp, Category: "cat", PF: "pkg-1", Split: true, Now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteRecord(1, "job", "src_install", "result", "", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Finalize(true); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "build", "cat", "pkg-1:20260102-030405.log.gz")
	if manager.Path() != want {
		t.Fatalf("path = %q, want %q", manager.Path(), want)
	}
	linked, err := os.Readlink(filepath.Join(temp, "build.log"))
	if err != nil || linked != want {
		t.Fatalf("canonical=%q err=%v", linked, err)
	}
	file, err := os.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	content, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "message=\"ok\"") {
		t.Fatalf("compressed records = %s", content)
	}
}

func TestPackageLogFailsClosedOnCollisionAndDuplicateFinalize(t *testing.T) {
	base, temp := t.TempDir(), filepath.Join(t.TempDir(), "T")
	options := PackageLogOptions{Root: base, TempDir: temp, Category: "cat", PF: "pkg-1", Now: time.Unix(0, 0)}
	manager, err := NewPackageLog(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPackageLog(PackageLogOptions{Root: base, TempDir: filepath.Join(t.TempDir(), "other-T"), Category: "cat", PF: "pkg-1", Now: time.Unix(0, 0)}); err == nil {
		t.Fatal("colliding durable log was accepted")
	}
	if err := manager.Finalize(false); err != nil {
		t.Fatal(err)
	}
	if err := manager.Finalize(false); err == nil {
		t.Fatal("duplicate finalization was accepted")
	}
}

func TestPackageLogFilterCommandTransformsStreamAndFailsClosed(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		manager, err := NewPackageLog(PackageLogOptions{Root: t.TempDir(), TempDir: t.TempDir(), Category: "cat", PF: "pkg-1", Now: time.Unix(2, 0), FilterCommand: []string{"/bin/sed", "s/secret/redacted/g"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.WriteRecord(1, "job", "src_compile", "log", "stdout", "secret"); err != nil {
			t.Fatal(err)
		}
		if err := manager.Finalize(false); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(manager.Path())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "secret") || !strings.Contains(string(content), "redacted") {
			t.Fatalf("filtered log = %s", content)
		}
	})
	t.Run("failure", func(t *testing.T) {
		manager, err := NewPackageLog(PackageLogOptions{Root: t.TempDir(), TempDir: t.TempDir(), Category: "cat", PF: "pkg-1", Now: time.Unix(3, 0), FilterCommand: []string{"/bin/sh", "-c", "cat; exit 7"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.WriteRecord(1, "job", "src_compile", "log", "stdout", "preserved"); err != nil {
			t.Fatal(err)
		}
		if err := manager.Finalize(false); err == nil || !strings.Contains(err.Error(), "filter failed") {
			t.Fatalf("Finalize error = %v", err)
		}
		content, err := os.ReadFile(manager.Path())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "preserved") {
			t.Fatalf("failure did not preserve log: %s", content)
		}
	})
}

func TestInterruptedPackageLogsPreserveAndClearDurableEvidence(t *testing.T) {
	root := t.TempDir()
	first, err := NewPackageLog(PackageLogOptions{Root: root, TempDir: t.TempDir(), Category: "cat", PF: "first-1", Now: time.Unix(10, 0)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPackageLog(PackageLogOptions{Root: root, TempDir: t.TempDir(), Category: "cat", PF: "second-1", Now: time.Unix(11, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.WriteRecord(1, "first", "src_compile", "log", "stdout", "durable before interruption"); err != nil {
		t.Fatal(err)
	}
	if err := second.WriteRecord(1, "second", "src_compile", "result", "", "complete"); err != nil {
		t.Fatal(err)
	}
	if err := second.Finalize(false); err != nil {
		t.Fatal(err)
	}
	paths, err := InterruptedPackageLogs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{first.Path()}) {
		t.Fatalf("interrupted paths = %#v", paths)
	}
	content, err := os.ReadFile(first.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "durable before interruption") {
		t.Fatalf("interrupted log = %s", content)
	}
	if err := first.Finalize(false); err != nil {
		t.Fatal(err)
	}
	paths, err = InterruptedPackageLogs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("finalized logs still active: %#v", paths)
	}
}
