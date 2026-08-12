package restartneeded

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotAndNewlyDeletedContract(t *testing.T) {
	beforeRoot := t.TempDir()
	afterRoot := t.TempDir()
	writeProcess(t, beforeRoot, "2739", "/usr/sbin/sshd", "sshd", "100")
	writeProcess(t, afterRoot, "2739", "/usr/sbin/sshd (deleted)", "sshd", "100")

	got := NewlyDeleted(Snapshot(beforeRoot), Snapshot(afterRoot))
	if len(got) != 1 || got[0].PID != 2739 || got[0].Name != "sshd" {
		t.Fatalf("newly deleted processes = %#v", got)
	}
	warning := Warning(got)
	for _, required := range []string{"critical", "pid 2739 (sshd)", "sshd -t", "kill -HUP", "test a second connection"} {
		if !strings.Contains(warning, required) {
			t.Errorf("warning missing %q: %s", required, warning)
		}
	}
}

func TestNewlyDeletedRejectsUnrelatedAndAdversarialRecords(t *testing.T) {
	before := map[int]Process{
		1: {PID: 1, StartTime: "a", Executable: "/sbin/init"},
		2: {PID: 2, StartTime: "b", Executable: "/old (deleted)"},
		3: {PID: 3, StartTime: "c", Executable: "/usr/bin/old"},
		4: {PID: 4, StartTime: "d", Executable: "/usr/bin/same"},
	}
	after := map[int]Process{
		1: {PID: 1, StartTime: "reused", Executable: "/sbin/init (deleted)"},
		2: {PID: 2, StartTime: "b", Executable: "/old (deleted)"},
		3: {PID: 3, StartTime: "c", Executable: "/usr/bin/different (deleted)"},
		4: {PID: 4, StartTime: "d", Executable: "/usr/bin/same"},
		5: {PID: 5, StartTime: "e", Executable: "/new (deleted)"},
	}
	if got := NewlyDeleted(before, after); len(got) != 0 {
		t.Fatalf("unrelated records reported: %#v", got)
	}
	if got := Warning(nil); got != "" {
		t.Fatalf("empty warning = %q", got)
	}
}

func TestSnapshotSkipsMalformedAndSortsByPID(t *testing.T) {
	root := t.TempDir()
	writeProcess(t, root, "20", "/bin/twenty", "", "20")
	writeProcess(t, root, "3", "/bin/three", "three", "3")
	if err := os.Mkdir(filepath.Join(root, "4"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/bad", filepath.Join(root, "4", "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "4", "stat"), []byte("malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := Snapshot(root)
	after := map[int]Process{}
	for pid, process := range before {
		process.Executable += " (deleted)"
		after[pid] = process
	}
	got := NewlyDeleted(before, after)
	if len(got) != 2 || got[0].PID != 3 || got[1].PID != 20 || got[1].Name != "twenty" {
		t.Fatalf("sorted/fallback records = %#v", got)
	}
}

func writeProcess(t *testing.T, root, pid, executable, name, startTime string) {
	t.Helper()
	directory := filepath.Join(root, pid)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(directory, "exe")); err != nil {
		t.Fatal(err)
	}
	fields := []string{"S"}
	for len(fields) < 20 {
		fields = append(fields, "0")
	}
	fields[19] = startTime
	stat := pid + " (a process ) name) " + strings.Join(fields, " ") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		if err := os.WriteFile(filepath.Join(directory, "comm"), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
