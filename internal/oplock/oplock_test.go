package oplock

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPortageLockPath(t *testing.T) {
	if got := PortageLockPath("/var/db/pkg"); got != "/var/db/.pkg.portage_lockfile" {
		t.Fatalf("PortageLockPath = %q", got)
	}
}

func TestPortageLockPathContract(t *testing.T) {
	tests := []struct {
		name      string
		directory string
		want      string
	}{
		{"trailing separator", "/var/db/pkg/", "/var/db/.pkg.portage_lockfile"},
		{"relative", "var/db/pkg", "var/db/.pkg.portage_lockfile"},
		{"dot elements", "/var/lib/portage/../portage/world", "/var/lib/portage/.world.portage_lockfile"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PortageLockPath(test.directory); got != test.want {
				t.Fatalf("PortageLockPath(%q) = %q, want %q", test.directory, got, test.want)
			}
		})
	}
}

func TestMutationTryAcquirePathCreatesParentWithPortageSiblingName(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "nested", "state", "world")
	lock, err := TryAcquirePath(statePath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "nested", "state", ".world.portage_lockfile")
	if got := lock.Path(); got != want {
		t.Fatalf("lock path = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("lock mode = %v, want regular file", info.Mode())
	}
	if _, err := os.Stat(filepath.Dir(want)); err != nil {
		t.Fatalf("lock parent was not created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseContractIsNilSafeAndIdempotent(t *testing.T) {
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil Release error = %v", err)
	}
	if got := nilLock.Path(); got != "" {
		t.Fatalf("nil Path = %q", got)
	}

	lock, err := TryAcquirePath(filepath.Join(t.TempDir(), "world"))
	if err != nil {
		t.Fatal(err)
	}
	path := lock.Path()
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release error = %v", err)
	}
	if got := lock.Path(); got != path {
		t.Fatalf("Path after release = %q, want stable identity %q", got, path)
	}

	reacquired, err := TryAcquirePath(filepath.Join(filepath.Dir(path), "world"))
	if err != nil {
		t.Fatalf("lock could not be reacquired after idempotent release: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestVDBLockContendsAcrossProcessesAndReleases(t *testing.T) {
	vdb := filepath.Join(t.TempDir(), "var", "db", "pkg")
	lock, err := TryAcquireVDB(vdb)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Path() != filepath.Join(filepath.Dir(vdb), ".pkg.portage_lockfile") {
		t.Fatalf("lock path = %q", lock.Path())
	}

	command := exec.Command(os.Args[0], "-test.run=TestVDBLockHelper")
	command.Env = append(os.Environ(), "ARISE_LOCK_HELPER=1", "ARISE_LOCK_VDB="+vdb)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Portage VDB is busy") {
		t.Fatalf("contending helper err=%v output=%s", err, output)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}

	command = exec.Command(os.Args[0], "-test.run=TestVDBLockHelper")
	command.Env = append(os.Environ(), "ARISE_LOCK_HELPER=1", "ARISE_LOCK_VDB="+vdb)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("released lock remained busy: %v: %s", err, output)
	}
}

func TestAcquireVDBWaitsAcrossProcessesAndThenSucceeds(t *testing.T) {
	vdb := filepath.Join(t.TempDir(), "var", "db", "pkg")
	lock, err := TryAcquireVDB(vdb)
	if err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=TestVDBLockHelper")
	command.Env = append(os.Environ(), "ARISE_LOCK_HELPER=wait", "ARISE_LOCK_VDB="+vdb)
	done := make(chan error, 1)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- command.Wait() }()

	select {
	case err := <-done:
		t.Fatalf("waiting lock helper exited before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waiting lock helper failed after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		t.Fatal("waiting lock helper did not acquire released lock")
	}
}

func TestVDBLockHelper(t *testing.T) {
	mode := os.Getenv("ARISE_LOCK_HELPER")
	if mode != "1" && mode != "wait" {
		return
	}
	var lock *Lock
	var err error
	if mode == "wait" {
		lock, err = AcquireVDB(os.Getenv("ARISE_LOCK_VDB"))
	} else {
		lock, err = TryAcquireVDB(os.Getenv("ARISE_LOCK_VDB"))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}
