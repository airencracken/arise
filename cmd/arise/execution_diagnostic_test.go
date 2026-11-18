package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/executor"
	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/rebuild"
)

func TestCompilerFailureExplainsWhatConfigLogContains(t *testing.T) {
	err := &rebuild.PhaseError{Package: "sys-libs/libxcrypt-4.5.2-r1", Phase: "src_configure", Err: errors.New("exit status 77"), Output: []string{"configure: error: C compiler cannot create executables", "configure: error: in '/var/tmp/arise/build/abi_x86_32'"}, LogFile: "/var/log/portage/libxcrypt.log"}
	var output bytes.Buffer
	printExecutionError(&output, err)
	for _, want := range []string{"C compiler could not link a test program", "sys-libs/libxcrypt", "config.log", "compiler command and linker error", "/var/log/portage/libxcrypt.log"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "phase protocol") {
		t.Fatalf("internal protocol used as explanation:\n%s", output.String())
	}
}

func TestPostInstallFailureReportsCommittedStateAndDoesNotSuggestReinstall(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &merge.PostCommitError{Err: &rebuild.PhaseError{Package: "cat/pkg-1", Phase: "pkg_postinst", Err: errors.New("exit status 1"), Output: []string{"service registration failed"}}})
	for _, want := range []string{"package changes applied; follow-up step failed", "already been committed", "Complete the failed follow-up step", "service registration failed"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "rerun the original command") || strings.Contains(output.String(), "package transaction failed") {
		t.Fatalf("committed package presented as retryable failed merge:\n%s", output.String())
	}
}

func TestConcurrentErrorsRetainEachPackageAndLog(t *testing.T) {
	err := errors.Join(
		&rebuild.PhaseError{Package: "cat/first-1", Phase: "src_compile", Err: errors.New("exit status 1"), Output: []string{"first compiler error"}, LogFile: "/logs/first.log"},
		&rebuild.PhaseError{Package: "cat/second-2", Phase: "src_test", Err: errors.New("exit status 2"), Output: []string{"second test failure"}, LogFile: "/logs/second.log"},
	)
	var output bytes.Buffer
	printExecutionError(&output, err)
	for _, want := range []string{"cat/first-1", "cat/second-2", "first compiler error", "second test failure", "/logs/first.log", "/logs/second.log"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("joined failure lost %q:\n%s", want, output.String())
		}
	}
}

func TestFailedRollbackRequiresRecoveryAndRetainsBothCauses(t *testing.T) {
	first, second := errors.New("payload write failed"), errors.New("restore preimage failed")
	err := &journal.RollbackError{Operation: "merge", Directory: "/journals/operation-1", Cause: first, Err: second}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatal("rollback error hid one of its causes")
	}
	var output bytes.Buffer
	printExecutionError(&output, err)
	for _, want := range []string{"automatic rollback could not finish", "arise recover status", "/journals/operation-1", first.Error(), second.Error()} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("recovery guidance lost %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "rerun the original command") {
		t.Fatalf("incomplete rollback was presented as safe to retry:\n%s", output.String())
	}
}

func TestIncompatibleBinaryRequestsSourceBuild(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, binpkg.BinaryExecutionABIError("6"))
	for _, want := range []string{"incompatible version of Arise", "Rebuild the package from source", "--usepkgonly"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("binary guidance lost %q:\n%s", want, output.String())
		}
	}
}

func TestFilesystemReasonComesFromErrorNotWordsInPath(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &os.PathError{Op: "open", Path: "/tmp/no space left on device", Err: syscall.EACCES})
	if !strings.Contains(output.String(), "access permissions") || strings.Contains(output.String(), "storage is full") {
		t.Fatalf("path text overrode the real cause:\n%s", output.String())
	}
}

func TestUnsupportedRecoveryFormatExplainsVersionCompatibility(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &binpkg.UnsupportedRecoverySchemaError{Schema: 99})
	for _, want := range []string{"cannot read this recovery backup format", "99", "compatible version"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("format guidance lost %q:\n%s", want, output.String())
		}
	}
}

func TestSavedEnvironmentSyntaxFailureIsReportedAsReloadFailure(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &rebuild.PhaseError{Package: "cat/pkg-1", Phase: "pkg_preinst", Err: errors.New("exit status 2"), Output: []string{"/var/tmp/arise/build/phase.environment: line 413: syntax error near unexpected token '('"}})
	for _, want := range []string{"could not reload the saved build environment", "Report this error", "phase.environment"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("reload guidance lost %q:\n%s", want, output.String())
		}
	}
}

func TestBuildStorageFailureRequestsSpaceRatherThanCompilerRepair(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &rebuild.PhaseError{Package: "cat/pkg-1", Phase: "src_configure", Err: errors.New("exit status 77"), Output: []string{"write conftest.c: no space left on device", "C compiler cannot create executables"}})
	if !strings.Contains(output.String(), "Build storage ran out of space") || !strings.Contains(output.String(), "Free space or quota") || strings.Contains(output.String(), "Read config.log") {
		t.Fatalf("storage failure was misdiagnosed:\n%s", output.String())
	}
}

func TestMultiplePostInstallFailuresKeepCommittedStateAndEachCause(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &merge.PostCommitError{Err: errors.Join(errors.New("first follow-up failed"), errors.New("second follow-up failed"))})
	for _, want := range []string{"first follow-up failed", "second follow-up failed", "already been committed"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("post-install explanation lost %q:\n%s", want, output.String())
		}
	}
}

func TestChangedPlanProvidesAnExecutableNextStep(t *testing.T) {
	for _, resume := range []bool{false, true} {
		var output bytes.Buffer
		printExecutionError(&output, &planChangeError{reason: "Another operation changed a required dependency.", resume: resume})
		want := "Rerun the original command"
		if resume {
			want = "arise --resume"
		}
		if !strings.Contains(output.String(), "plan must be recalculated") || !strings.Contains(output.String(), want) {
			t.Fatalf("changed-plan diagnostic = %s", output.String())
		}
	}
}

func TestFailedRollbackKeepsSecondCauseAfterLongOriginalOutput(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, &journal.RollbackError{Operation: "merge", Directory: "/journals/operation-1", Cause: errors.New(strings.Repeat("original error output\n", 40)), Err: errors.New("rollback could not restore /usr/bin/tool")})
	if !strings.Contains(output.String(), "original error output") || !strings.Contains(output.String(), "rollback could not restore /usr/bin/tool") {
		t.Fatalf("one of the rollback causes was hidden:\n%s", output.String())
	}
}

func TestUnknownExecutionFailureRetainsPackageIdentity(t *testing.T) {
	cause := errors.New("required tool returned an unexpected result")
	err := &executor.ActionError{Package: "app-arch/gzip-1.15", Err: cause}
	if !errors.Is(err, cause) {
		t.Fatal("package context hid the underlying cause")
	}
	var output bytes.Buffer
	printExecutionError(&output, err)
	for _, want := range []string{"Current package action: app-arch/gzip-1.15", cause.Error(), "Next step:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("unknown error lost %q:\n%s", want, output.String())
		}
	}
}

func TestSingleJoinedPostCommitFailureRetainsOutcomeAndPackage(t *testing.T) {
	var output bytes.Buffer
	err := &executor.ActionError{Package: "cat/pkg-1", Err: errors.Join(&merge.PostCommitError{Err: errors.New("follow-up failed")})}
	printExecutionError(&output, err)
	if !strings.Contains(output.String(), "package changes applied; follow-up step failed") ||
		!strings.Contains(output.String(), "Current package action: cat/pkg-1") ||
		strings.Contains(output.String(), "package transaction failed") {
		t.Fatalf("single joined cause lost committed outcome:\n%s", output.String())
	}
}

func TestDefaultPreflightDiagnosticShowsEbuildRequirements(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	pkg := filepath.Join(repo, "www-client", "firefox")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	const requirement = "There is NOT at least 9700 MiB disk space at"
	const other = "Enable the required kernel option before rebuilding."
	recipe := `EAPI=8
SLOT=0
pkg_pretend() {
	eerror 'There is NOT at least 9700 MiB disk space at' "$T"
	eerror '- add more space'
	for i in {1..12}; do einfo "check $i"; done
	eerror 'Enable the required kernel option before rebuilding.'
	die 'Build requirements not met!'
}
`
	if err := os.WriteFile(filepath.Join(pkg, "firefox-153.7.0.ebuild"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &rebuild.RebuildConfig{RepoDir: repo, Repository: "gentoo", RootDir: filepath.Join(root, "target"),
		VdbDir: filepath.Join(root, "target/var/db/pkg"), WorkDirBase: filepath.Join(root, "work"), PhaseLogDir: filepath.Join(root, "logs"), JournalDir: filepath.Join(root, "journal")}
	err := rebuild.PreflightPackage("www-client/firefox-153.7.0", cfg)
	if err == nil {
		t.Fatal("failing pkg_pretend passed")
	}
	for _, verbose := range []bool{false, true} {
		var out bytes.Buffer
		printExecutionErrorWithOptions(&out, fmt.Errorf("executor: preflight www-client/firefox-153.7.0: %w", err), verbose)
		for _, want := range []string{"www-client/firefox-153.7.0", "pkg_pretend", requirement, cfg.WorkDirBase, "- add more space", other, "Build requirements not met!"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("verbose=%t: missing %q in %s", verbose, want, out.String())
			}
		}
		if strings.Contains(out.String(), "Technical details:") != verbose {
			t.Fatalf("verbose=%t: unexpected technical detail section in %s", verbose, out.String())
		}
	}
}

func TestUnknownCommandFailureShowsOperationAndStderr(t *testing.T) {
	err := fmt.Errorf("verify artifact signature: %w; stderr: public key unavailable", errors.New("exit status 1"))
	var output bytes.Buffer
	printExecutionError(&output, fmt.Errorf("executor: preflight cat/example-1: %w", err))
	for _, want := range []string{"cat/example-1", "verify artifact signature", "public key unavailable", "exit status 1"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q from %s", want, output.String())
		}
	}
}
