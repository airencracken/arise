package support

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleasedCLISyncStopsOnInterrupt(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release CLI: %v\n%s", err, output)
	}
	configRoot := filepath.Join(root, "etc", "portage")
	repository := filepath.Join(root, "repository")
	binDir := filepath.Join(root, "bin")
	for _, directory := range []string{configRoot, repository, binDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reposConf := "[interrupt-test]\nlocation = " + repository +
		"\nsync-type = rsync\nsync-uri = rsync://example.invalid/repository\n"
	if err := os.WriteFile(filepath.Join(configRoot, "repos.conf"), []byte(reposConf), 0o644); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(root, "rsync-started")
	rsync := "#!/bin/sh\ntouch '" + started + "'\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(binDir, "rsync"), []byte(rsync), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary,
		"--repo", repository,
		"--db", filepath.Join(root, "db"),
		"--portage-config-root", configRoot,
		"sync", "interrupt-test",
	)
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatalf("sync transport did not start:\n%s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	interrupted := time.Now()
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case err := <-waited:
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 130 {
			t.Fatalf("sync exit = %v, want status 130:\n%s", err, output.String())
		}
		if !strings.Contains(output.String(), "sync: interrupted by user") {
			t.Fatalf("sync output omitted interruption:\n%s", output.String())
		}
		if elapsed := time.Since(interrupted); elapsed > 5*time.Second {
			t.Fatalf("sync took %s to stop after interrupt", elapsed)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("sync ignored interrupt:\n%s", output.String())
	}
}
