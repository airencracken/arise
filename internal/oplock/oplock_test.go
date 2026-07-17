package oplock

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortageLockPath(t *testing.T) {
	if got := PortageLockPath("/var/db/pkg"); got != "/var/db/.pkg.portage_lockfile" {
		t.Fatalf("PortageLockPath = %q", got)
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

func TestVDBLockHelper(t *testing.T) {
	if os.Getenv("ARISE_LOCK_HELPER") != "1" {
		return
	}
	lock, err := TryAcquireVDB(os.Getenv("ARISE_LOCK_VDB"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}
