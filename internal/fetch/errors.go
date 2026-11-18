package fetch

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/airencracken/arise/internal/diagnostic"
)

func (e *ManualFetchRequiredError) UserDiagnostic() diagnostic.Message {
	return diagnostic.Message{
		Summary:   fmt.Sprintf("The ebuild requires a manual download of %s (RESTRICT=fetch).", e.Artifact),
		Details:   []string{e.Cause.Error()},
		NextSteps: []string{fmt.Sprintf("Obtain the file from the package provider and place the Manifest-matching copy at %q, then retry.", e.Destination)},
	}
}

type sourcesFailedError struct {
	artifact, destination string
	failures              []error
}

func (e *sourcesFailedError) Error() string {
	return fmt.Sprintf("fetch: all sources failed for %s: %v", e.artifact, e.failures)
}
func (e *sourcesFailedError) Unwrap() []error { return e.failures }

func (e *sourcesFailedError) UserDiagnostic() diagnostic.Message {
	message := diagnostic.Message{
		Summary:   fmt.Sprintf("Could not fetch a verified copy of %s.", e.artifact),
		Context:   []string{fmt.Sprintf("Distfile destination: %q", e.destination)},
		NextSteps: []string{"Check the listed source errors, then retry or place a Manifest-matching copy at the distfile destination."},
	}
	for _, err := range e.failures {
		message.Details = append(message.Details, err.Error())
	}
	var pathErr *os.PathError
	if errors.As(e, &pathErr) {
		switch {
		case errors.Is(pathErr, syscall.ENOSPC), errors.Is(pathErr, syscall.EDQUOT):
			message.NextSteps = []string{fmt.Sprintf("Free space or quota on the filesystem containing %q, then retry.", pathErr.Path)}
		case errors.Is(pathErr, os.ErrPermission):
			message.NextSteps = []string{fmt.Sprintf("Check access permissions for %q, then retry.", pathErr.Path)}
		}
	}
	return message
}
