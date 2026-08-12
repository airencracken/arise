// Package restartneeded detects processes whose executable was replaced during
// a package transaction. Such processes keep running the unlinked image and
// may fail in surprising ways until their service is reloaded or restarted.
package restartneeded

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const deletedSuffix = " (deleted)"

// Process is the stable portion of a /proc process record needed for a
// before/after comparison.
type Process struct {
	PID        int
	StartTime  string
	Executable string
	Name       string
}

// Snapshot records processes visible below procRoot. Permission failures and
// processes which disappear during the scan are intentionally skipped.
func Snapshot(procRoot string) map[int]Process {
	result := make(map[int]Process)
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		base := filepath.Join(procRoot, entry.Name())
		executable, err := os.Readlink(filepath.Join(base, "exe"))
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(base, "stat"))
		if err != nil {
			continue
		}
		startTime, ok := parseStartTime(string(stat))
		if !ok {
			continue
		}
		name := processName(base, executable)
		result[pid] = Process{PID: pid, StartTime: startTime, Executable: executable, Name: name}
	}
	return result
}

// NewlyDeleted returns processes that survived the transaction with the same
// identity but changed from a linked executable to an unlinked one.
func NewlyDeleted(before, after map[int]Process) []Process {
	var result []Process
	for pid, current := range after {
		previous, existed := before[pid]
		if !existed || previous.StartTime != current.StartTime {
			continue
		}
		if strings.HasSuffix(previous.Executable, deletedSuffix) || !strings.HasSuffix(current.Executable, deletedSuffix) {
			continue
		}
		if strings.TrimSuffix(current.Executable, deletedSuffix) != previous.Executable {
			continue
		}
		result = append(result, current)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result
}

// Warning formats operator guidance without attempting a potentially unsafe
// automatic restart of a remote-access or other critical service.
func Warning(processes []Process) string {
	if len(processes) == 0 {
		return ""
	}
	var output strings.Builder
	fmt.Fprintf(&output, "arise: critical: %d running process(es) still use executables replaced by this transaction:\n", len(processes))
	for _, process := range processes {
		fmt.Fprintf(&output, "  pid %d (%s): %s\n", process.PID, process.Name, process.Executable)
	}
	output.WriteString("These processes require a service reload or restart. Verify replacement daemons before closing this session.\n")
	output.WriteString("For sshd, validate the new binary with `sshd -t`, keep this session open, then reload the existing listener (for example `kill -HUP <pid>`) and test a second connection.\n")
	return output.String()
}

func parseStartTime(stat string) (string, bool) {
	// The comm field is parenthesized and may itself contain spaces or ')'. The
	// remaining fields begin after the last ") "; starttime is field 22, or
	// index 19 in the remainder beginning with field 3 (state).
	end := strings.LastIndex(stat, ") ")
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(stat[end+2:])
	if len(fields) <= 19 {
		return "", false
	}
	return fields[19], true
}

func processName(base, executable string) string {
	if content, err := os.ReadFile(filepath.Join(base, "comm")); err == nil {
		if name := strings.TrimSpace(string(content)); name != "" {
			return name
		}
	}
	return filepath.Base(strings.TrimSuffix(executable, deletedSuffix))
}
