package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/airencracken/arise/internal/diagnostic"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/oplock"
)

func executionDiagnostic(err error) diagnostic.Message {
	message, explained := diagnostic.Lookup(err)
	var committed *merge.PostCommitError
	if errors.As(err, &committed) {
		message.Summary = "Package changes were applied, but a follow-up step failed."
		message.Context = append(message.Context, "The package files and database changes have already been committed.")
		message.NextSteps = []string{"Complete the failed follow-up step described in the output or package log."}
		if len(message.Details) == 0 {
			message.Details = []string{committed.Err.Error()}
		}
		return message
	}
	if explained {
		return message
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
			return diagnostic.Message{Summary: "Required storage is full.", Context: []string{fmt.Sprintf("Affected path: %q", pathError.Path)}, Details: []string{pathError.Err.Error()}, NextSteps: []string{"Free space or quota on the filesystem containing this path, then rerun the original command."}}
		}
		message = diagnostic.Message{Summary: "Arise could not access a required file or directory.", Context: []string{fmt.Sprintf("Affected path: %q", pathError.Path)}, Details: []string{pathError.Err.Error()}}
		switch {
		case errors.Is(err, os.ErrPermission):
			message.NextSteps = []string{"Check access permissions for the affected path before retrying."}
		case errors.Is(err, os.ErrNotExist):
			message.NextSteps = []string{"Check why the required path is missing; restore it or correct its configuration before retrying."}
		default:
			message.NextSteps = []string{"Check the affected path and reported filesystem error before retrying."}
		}
		return message
	}
	if hint := executionStorageHint(err); hint != "" {
		leaf := err
		for errors.Unwrap(leaf) != nil {
			leaf = errors.Unwrap(leaf)
		}
		return diagnostic.Message{Summary: "Temporary build storage is full.", Details: []string{leaf.Error()}, NextSteps: []string{strings.TrimPrefix(hint, "Temporary build storage is full. ")}}
	}
	if errors.Is(err, oplock.ErrBusy) {
		return diagnostic.Message{Summary: "Another package operation is using the package database.", NextSteps: []string{"Let that operation finish, then rerun the original command."}}
	}
	leaf := err
	for errors.Unwrap(leaf) != nil {
		leaf = errors.Unwrap(leaf)
	}
	message = diagnostic.Message{Summary: "The package operation could not be completed.", Details: []string{leaf.Error()}, NextSteps: []string{"Read the error details and any log below; include them when reporting this problem."}}
	return message
}

func printExecutionErrorWithOptions(w io.Writer, err error, verbose bool) {
	heading := "arise: package transaction failed"
	for current := err; current != nil; current = singleExecutionCause(current) {
		if _, ok := current.(*merge.PostCommitError); ok {
			heading = "arise: package changes applied; follow-up step failed"
			break
		}
	}
	printCommandError(w, heading, err, verbose)
}

func singleExecutionCause(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		if causes := joined.Unwrap(); len(causes) == 1 {
			return causes[0]
		}
		return nil
	}
	return errors.Unwrap(err)
}

func printCommandError(w io.Writer, heading string, err error, verbose bool) {
	technical, logFile := executionErrorDetails(err)
	fmt.Fprintln(w, heading)
	printExecutionExplanation(w, err, logFile)
	if verbose {
		fmt.Fprintln(w, "\n  Technical details:")
		for _, line := range technical {
			fmt.Fprintf(w, "    %s\n", line)
		}
	} else {
		fmt.Fprintln(w, "\n  Use --verbose for technical details.")
	}
	fmt.Fprintln(w)
}

func printExecutionExplanation(w io.Writer, err error, fallbackLog string) {
	// Preserve each branch of a concurrent failure. A single errors.As lookup
	// would otherwise hide the other package's cause and recovery instructions.
	for current := err; current != nil; current = singleExecutionCause(current) {
		if _, ok := current.(diagnostic.Reporter); ok {
			break
		}
		if committed, ok := current.(*merge.PostCommitError); ok {
			if joined, ok := committed.Err.(interface{ Unwrap() []error }); ok && len(joined.Unwrap()) > 1 {
				for _, cause := range joined.Unwrap() {
					_, childLog := executionErrorDetails(cause)
					printExecutionExplanation(w, &merge.PostCommitError{Err: cause}, childLog)
				}
				return
			}
			break
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			if len(joined.Unwrap()) == 1 {
				continue
			}
			for _, cause := range joined.Unwrap() {
				_, childLog := executionErrorDetails(cause)
				printExecutionExplanation(w, cause, childLog)
			}
			return
		}
	}
	message := executionDiagnostic(err)
	var packageFailure interface{ FailedPackage() string }
	if errors.As(err, &packageFailure) {
		message.Context = append(message.Context, "Current package action: "+packageFailure.FailedPackage())
	}
	logFile := fallbackLog
	if message.LogFile != "" {
		logFile = message.LogFile
	}
	fmt.Fprintf(w, "\n  %s\n", message.Summary)
	for _, line := range message.Context {
		fmt.Fprintf(w, "  %s\n", line)
	}
	if len(message.Details) != 0 {
		fmt.Fprintln(w, "\n  Details:")
		for _, detailText := range message.Details {
			if start := strings.LastIndex(detailText, "(durable log: "); start >= 0 {
				if end := strings.Index(detailText[start:], ")"); end >= 0 {
					detailText = detailText[:start] + detailText[start+end+1:]
				}
			}
			lines := executionDiagnosticLines(strings.Split(detailText, "\n"))
			for index, line := range lines {
				if index >= 10 {
					break
				}
				if strings.TrimSpace(line) != "" {
					fmt.Fprintf(w, "    %s\n", strings.TrimSpace(line))
				}
			}
		}
	}
	for _, step := range message.NextSteps {
		fmt.Fprintf(w, "\n  Next step: %s\n", step)
	}
	if logFile != "" {
		fmt.Fprintf(w, "\n  Log file:\n    '%s'\n", logFile)
	}
}
