package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/bugreport"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/resolvertrace"
)

type repeatedString []string

func (values *repeatedString) String() string { return strings.Join(*values, ",") }
func (values *repeatedString) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("value cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

func runBugReport(args []string) int {
	options := flag.NewFlagSet("bug-report", flag.ContinueOnError)
	options.SetOutput(os.Stderr)
	output := options.String("output", "arise-bug-report", "new directory for reviewable report files")
	archive := options.String("archive", "", "optional deterministic .tar.zst output path")
	pkg := options.String("package", "", "explicit package associated with the failure")
	planDigest := options.String("plan-sha256", "", "approved plan digest associated with the failure")
	latestFailure := options.Bool("latest-failure", true, "include the latest interrupted package log")
	resolverTracePath := options.String("resolver-trace", "", "validated resolver trace to redact again and embed")
	var logs repeatedString
	options.Var(&logs, "log", "explicit durable log path (repeatable)")
	if err := options.Parse(args); err != nil {
		return 2
	}
	if options.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "bug-report: unexpected positional arguments")
		return 2
	}
	if *archive != "" && !strings.HasSuffix(*archive, ".tar.zst") {
		fmt.Fprintln(os.Stderr, "bug-report: archive path must end in .tar.zst")
		return 2
	}
	if *latestFailure {
		if paths, err := phaseproto.InterruptedPackageLogs(commandRootPath("/var/log/portage")); err == nil && len(paths) != 0 {
			sort.Strings(paths)
			logs = append(logs, paths[len(paths)-1])
		}
	}
	redactor := bugreport.NewRedactor()
	report := bugreport.Collect(bugreport.Options{
		Version: version, Package: *pkg, PlanSHA256: *planDigest, Invocation: os.Args,
		ResumePath: *resumeFile, JournalDir: *journalDir,
		FilesystemPaths: []string{commandEnv("ROOT", "/"), commandEnv("PORTAGE_TMPDIR", "/var/tmp"), commandEnv("DISTDIR", "/var/cache/distfiles")},
		LogPaths:        logs, Redactor: redactor,
	})
	if *resolverTracePath != "" {
		traceFile, openErr := os.Open(*resolverTracePath)
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "bug-report: open resolver trace: %v\n", openErr)
			return 1
		}
		trace, decodeErr := resolvertrace.Decode(traceFile)
		closeErr := traceFile.Close()
		if decodeErr != nil {
			fmt.Fprintf(os.Stderr, "bug-report: decode resolver trace: %v\n", decodeErr)
			return 1
		}
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "bug-report: close resolver trace: %v\n", closeErr)
			return 1
		}
		var encoded strings.Builder
		if encodeErr := resolvertrace.Encode(&encoded, resolvertrace.Sanitize(trace, redactor)); encodeErr != nil {
			fmt.Fprintf(os.Stderr, "bug-report: encode resolver trace: %v\n", encodeErr)
			return 1
		}
		report.ResolverTrace = json.RawMessage(encoded.String())
	}
	if err := bugreport.WriteDirectory(*output, report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *archive != "" {
		if err := writeBugReportArchive(*archive, report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Printf("Bug report written to %s for review. Nothing was uploaded.\n", *output)
	return 0
}

func writeBugReportArchive(path string, report bugreport.Report) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("bug-report: archive already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bug-report: inspect archive: %w", err)
	}
	parent := filepath.Dir(path)
	file, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("bug-report: create archive staging file: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("bug-report: secure archive staging file: %w", err)
	}
	writeErr := bugreport.WriteArchive(file, report)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("bug-report: write archive: %w", writeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("bug-report: publish archive: %w", err)
	}
	return nil
}
