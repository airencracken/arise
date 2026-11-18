package rebuild

import (
	"fmt"
	"strings"

	"github.com/airencracken/arise/internal/diagnostic"
	"github.com/airencracken/arise/internal/phaseproto"
)

func phaseExecutionError(pkg, phase string, events []phaseproto.Event, logFile string, err error) *PhaseError {
	failure := &PhaseError{Package: pkg, Phase: phase, LogFile: logFile, Err: err}
	// eerror is explicit package-author guidance. Retain it even when the
	// compiler output fills the excerpt or the guidance contains no keyword.
	explicit := make(map[string]bool)
	for _, event := range events {
		if event.Kind == "elog" && event.Class == "ERROR" && strings.TrimSpace(event.Message) != "" {
			explicit[event.Message] = true
			failure.Output = append(failure.Output, event.Message)
		}
	}
	for _, line := range phaseFailureDiagnostics(events, 20) {
		if !explicit[line] {
			failure.Output = append(failure.Output, line)
		}
	}
	return failure
}

// Preflight has no persistent build log. Preserve the ebuild's instructions
// in order, including lines a compiler-error filter would otherwise discard.
func preflightPhaseError(pkg string, events []phaseproto.Event, err error, tempDir, workDir string) *PhaseError {
	failure := &PhaseError{Package: pkg, Phase: "pkg_pretend", Err: err}
	for _, event := range events {
		if (event.Kind == "elog" || event.Kind == "log") && strings.TrimSpace(event.Message) != "" {
			failure.Output = append(failure.Output, strings.Split(event.Message, "\n")...)
		}
	}
	failure.spaceAdvice = preflightSpaceAdvice(failure.Output, tempDir, workDir)
	return failure
}

type PhaseError struct {
	Package     string
	Phase       string
	Output      []string
	LogFile     string
	Err         error
	spaceAdvice string
}

func (e *PhaseError) Error() string {
	message := fmt.Sprintf("rebuild: phase protocol %s: %v", e.Phase, e.Err)
	if len(e.Output) != 0 {
		message += ": " + strings.Join(e.Output, "\n")
	}
	return message
}
func (e *PhaseError) Unwrap() error { return e.Err }

func (e *PhaseError) UserDiagnostic() diagnostic.Message {
	if e.Phase == "pkg_pretend" {
		details := e.Output
		if len(details) == 0 {
			details = []string{e.Err.Error()}
		}
		step := "Address the requirement reported by the ebuild, then rerun the original command."
		if e.spaceAdvice != "" {
			step = e.spaceAdvice
		}
		return diagnostic.Message{
			Summary:   fmt.Sprintf("Preflight failed for %s (pkg_pretend).", e.Package),
			Details:   details,
			NextSteps: []string{step},
		}
	}
	stages := map[string]string{
		"pkg_setup": "preparing the build environment", "src_unpack": "unpacking sources",
		"src_prepare": "preparing sources", "src_configure": "configuring the build",
		"src_compile": "compiling", "src_test": "running tests", "src_install": "preparing installation files",
		"pkg_preinst": "running pre-install checks", "pkg_postinst": "running post-install steps",
		"pkg_prerm": "running pre-removal steps", "pkg_postrm": "running post-removal steps",
	}
	stage := stages[e.Phase]
	if stage == "" {
		stage = strings.ReplaceAll(e.Phase, "_", " ")
	}
	message := diagnostic.Message{
		Summary: fmt.Sprintf("Failed while %s for %s.", stage, e.Package),
		Context: []string{"Package: " + e.Package}, Details: e.Output, LogFile: e.LogFile,
		NextSteps: []string{"Read the error output and build log, fix the reported cause, then rerun the original command."},
	}
	if len(e.Output) == 0 {
		message.Details = []string{e.Err.Error()}
	}
	output := strings.Join(e.Output, "\n")
	if strings.Contains(output, "phase.environment") && strings.Contains(output, "syntax error") {
		message.Summary = "Arise could not reload the saved build environment for " + e.Package + "."
		message.NextSteps = []string{"Report this error with the build log and the saved-environment filename shown below."}
		return message
	}
	if strings.Contains(strings.ToLower(output), "no space left on device") || strings.Contains(strings.ToLower(output), "disk quota exceeded") {
		message.Summary = "Build storage ran out of space while " + stage + " for " + e.Package + "."
		message.NextSteps = []string{"Free space or quota in the build storage shown in the error, then rerun the original command."}
		return message
	}
	if e.Phase == "src_configure" && strings.Contains(output, "C compiler cannot create executables") {
		message.Summary = "The C compiler could not link a test program while configuring " + e.Package + "."
		message.NextSteps = []string{"Read config.log in the build directory shown in the output; it contains the compiler command and linker error."}
	}
	return message
}
