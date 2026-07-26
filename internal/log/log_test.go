package log

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func capture(t *testing.T, configuredLevel slog.Level) *bytes.Buffer {
	t.Helper()
	output := &bytes.Buffer{}
	SetOutput(output)
	SetLevel(configuredLevel)
	t.Cleanup(func() {
		SetLevel(slog.LevelInfo)
		SetOutput(&bytes.Buffer{})
	})
	return output
}

func TestLevelContractFiltersMessagesBelowThreshold(t *testing.T) {
	tests := []struct {
		name       string
		initial    slog.Level
		configure  string
		write      func(string, ...any)
		message    string
		wantOutput bool
	}{
		{name: "debug enabled", initial: slog.LevelInfo, configure: "debug", write: Debug, message: "diagnostic", wantOutput: true},
		{name: "debug filtered at info", configure: "info", write: Debug, message: "diagnostic", wantOutput: false},
		{name: "info enabled", configure: "info", write: Info, message: "ready", wantOutput: true},
		{name: "info resets debug threshold", initial: slog.LevelDebug, configure: "info", write: Debug, message: "diagnostic", wantOutput: false},
		{name: "info filtered at warn", configure: "warn", write: Info, message: "ready", wantOutput: false},
		{name: "warn enabled", configure: "warn", write: Warn, message: "caution", wantOutput: true},
		{name: "warn filtered at error", configure: "error", write: Warn, message: "caution", wantOutput: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := capture(t, test.initial)
			SetLevelString(test.configure)
			test.write(test.message, "package", "sys-apps/portage")
			got := output.String()
			if strings.Contains(got, test.message) != test.wantOutput {
				t.Fatalf("output = %q, want message present %t", got, test.wantOutput)
			}
			if test.wantOutput && !strings.Contains(got, "package=sys-apps/portage") {
				t.Fatalf("structured attribute missing from %q", got)
			}
		})
	}
}

func TestDefaultLoggerContractInFreshProcess(t *testing.T) {
	if os.Getenv("ARISE_LOG_DEFAULT_HELPER") == "1" {
		Debug("default debug must be filtered")
		Info("default info must be visible")
		SetLevel(slog.LevelDebug)
		Debug("runtime debug must be visible")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDefaultLoggerContractInFreshProcess$")
	command.Env = append(os.Environ(), "ARISE_LOG_DEFAULT_HELPER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh-process logger contract: %v: %s", err, output)
	}
	got := string(output)
	if strings.Contains(got, "default debug must be filtered") ||
		!strings.Contains(got, "default info must be visible") ||
		!strings.Contains(got, "runtime debug must be visible") {
		t.Fatalf("default logger contract violated: %q", got)
	}
}

func TestSetLevelAcceptsSlogLevels(t *testing.T) {
	output := capture(t, slog.LevelError)
	Info("hidden")
	Error("visible")
	if got := output.String(); strings.Contains(got, "hidden") || !strings.Contains(got, "visible") {
		t.Fatalf("unexpected level filtering: %q", got)
	}
}

func TestUnknownLevelStringPreservesCurrentLevel(t *testing.T) {
	output := capture(t, slog.LevelWarn)
	SetLevelString("verbose")
	Info("must remain filtered")
	Warn("must remain visible")
	got := output.String()
	if strings.Contains(got, "must remain filtered") || !strings.Contains(got, "must remain visible") {
		t.Fatalf("unknown level changed threshold: %q", got)
	}
}

func TestErrorReturnsStableMessageAndLogsAttributes(t *testing.T) {
	output := capture(t, slog.LevelDebug)
	err := Error("transaction failed", "stage", "merge")
	if err == nil || err.Error() != "transaction failed" {
		t.Fatalf("Error() = %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "level=ERROR") ||
		!strings.Contains(got, `msg="transaction failed"`) ||
		!strings.Contains(got, "stage=merge") {
		t.Fatalf("error log contract violated: %q", got)
	}
}

func TestErrorfFormatsAndLogsTheSameError(t *testing.T) {
	output := capture(t, slog.LevelDebug)
	err := Errorf("package %s failed at phase %d", "cat/pkg", 3)
	const want = "package cat/pkg failed at phase 3"
	if err == nil || err.Error() != want {
		t.Fatalf("Errorf() = %v, want %q", err, want)
	}
	if !strings.Contains(output.String(), want) {
		t.Fatalf("formatted error missing from log: %q", output.String())
	}
}

func TestAdversarialOddStructuredArgumentsDoNotPanic(t *testing.T) {
	output := capture(t, slog.LevelDebug)
	for _, args := range [][]any{
		nil,
		{"key-without-value"},
		{42, "non-string-key"},
		{"nested", map[string]any{"token": nil}},
	} {
		Info("hostile input", args...)
	}
	if count := strings.Count(output.String(), "hostile input"); count != 4 {
		t.Fatalf("logged %d messages, want 4: %q", count, output.String())
	}
}
