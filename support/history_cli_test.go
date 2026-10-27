package support

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryReleasedCLIReadOnlyRoutesAndColor(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "arise")
	if output, err := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "../cmd/arise").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	log := filepath.Join(root, "emerge.log")
	data := []byte("1700000000:  >>> emerge (1 of 1) app-misc/demo-1 to /\n1700000060:  ::: completed emerge (1 of 1) app-misc/demo-1 to /\n")
	if err := os.WriteFile(log, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"history", "--log", log, "--json"}, {"--json", "history", "stats", "--log", log}, {"--color=y", "history", "--log", log, "--json"}, {"--emerge-log", log, "history", "--json"}} {
		command := exec.Command(binary, args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("%v: %v, %s", args, err, &stderr)
		}
		var report map[string]any
		if err := json.Unmarshal(output, &report); err != nil {
			t.Fatal(err)
		}
		if report["schema"] != "arise.history.v1" || report["matched"] != float64(1) || bytes.Contains(output, []byte("\x1b")) {
			t.Fatalf("report=%s", output)
		}
	}
	for _, mode := range []string{"auto", "y", "n"} {
		output, err := exec.Command(binary, "--color="+mode, "history", "--log", log, "--utc").Output()
		if err != nil {
			t.Fatal(err)
		}
		if colored := bytes.Contains(output, []byte("\x1b[")); colored != (mode == "y") {
			t.Fatalf("color %s: %q", mode, output)
		}
		if !bytes.Contains(output, []byte("1m 00s")) {
			t.Fatalf("duration missing: %s", output)
		}
	}
	if output, err := exec.Command(binary, "history", "--help").CombinedOutput(); err != nil || !bytes.Contains(output, []byte("--")) && !bytes.Contains(output, []byte("-since")) {
		t.Fatalf("help: %v, %s", err, output)
	}
	after, err := os.ReadFile(log)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("history mutated the input log")
	}
	for _, tool := range []string{"qlop", "genlop"} {
		if _, err := exec.LookPath(tool); err != nil {
			continue
		}
		args := []string{"-n", "-t", "-f", log, "app-misc/demo"}
		want := "merge time: 1 minute"
		if tool == "qlop" {
			args = []string{"--logfile", log, "--merge", "--time", "--machine", "app-misc/demo"}
			want = ": 60"
		}
		command := exec.Command(tool, args...)
		command.Env = append(os.Environ(), "LC_ALL=C")
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), want) {
			t.Fatalf("%s reference disagrees: %v, %s", tool, err, output)
		}
	}
	unmerge := filepath.Join(root, "unmerge.log")
	if err := os.WriteFile(unmerge, []byte("1700000100:  === Unmerging... (app-misc/demo-1)\n1700000160:  >>> unmerge success: app-misc/demo-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "history", "--kind", "unmerge", "--log", unmerge, "--json").Output()
	if err != nil || !bytes.Contains(output, []byte("\"duration_seconds\": 60")) {
		t.Fatalf("native unmerge timing: %v %s", err, output)
	}
	if _, err := exec.LookPath("qlop"); err == nil {
		output, err := exec.Command("qlop", "--logfile", unmerge, "--unmerge", "--autoclean", "--time", "--machine", "app-misc/demo").CombinedOutput()
		if err != nil || !strings.Contains(string(output), ": 60") {
			t.Fatalf("qlop unmerge reference: %v %s", err, output)
		}
	}
}

func TestHistorySchemaContract(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/history-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatal("schema contract missing")
	}
	properties := schema["properties"].(map[string]any)
	if properties["schema"].(map[string]any)["const"] != "arise.history.v1" {
		t.Fatal("schema identity missing")
	}
}
