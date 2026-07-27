package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestExitIfIndexInterruptedUsesShellInterruptStatus(t *testing.T) {
	if os.Getenv("ARISE_TEST_INDEX_INTERRUPT") == "1" {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		exitIfIndexInterrupted(ctx)
		os.Exit(99)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestExitIfIndexInterruptedUsesShellInterruptStatus$")
	command.Env = append(os.Environ(), "ARISE_TEST_INDEX_INTERRUPT=1")
	err := command.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupted index exit = %v, want status 130", err)
	}
}

func TestExitIfIndexInterruptedAllowsLiveContext(t *testing.T) {
	exitIfIndexInterrupted(context.Background())
}
