package restartneeded

import (
	"os"
	"strings"
	"testing"
)

func TestAuditNonSSHWarning(t *testing.T) {
	w := Warning([]Process{{PID: 123, Name: "nginx", Executable: "/usr/sbin/nginx (deleted)"}})
	if strings.Contains(w, "sshd") {
		t.Fatalf("nginx replacement prints unrelated SSH guidance: %s", w)
	}
}

func TestAuditSelfReplacementIsNotServiceEmergency(t *testing.T) {
	pid := os.Getpid()
	p := Process{PID: pid, Name: "arise", Executable: "/usr/bin/arise", StartTime: "100"}
	q := p
	q.Executable += " (deleted)"
	w := Warning(NewlyDeleted(map[int]Process{pid: p}, map[int]Process{pid: q}))
	if strings.Contains(w, "critical") || strings.Contains(w, "service reload or restart") {
		t.Fatalf("self-upgrade incorrectly treated as service emergency: %s", w)
	}
}
