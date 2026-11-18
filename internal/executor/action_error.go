package executor

import (
	"context"
	"errors"
	"fmt"

	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/merge"
)

// ActionError retains package identity independently of the technical wrapper.
type ActionError struct {
	Package string
	Err     error
}

func (e *ActionError) Error() string         { return fmt.Sprintf("executor: %s: %v", e.Package, e.Err) }
func (e *ActionError) Unwrap() error         { return e.Err }
func (e *ActionError) FailedPackage() string { return e.Package }

// OnlyCanceled distinguishes stopped workers from a real failure joined with
// cancellation. Committed or incompletely rolled-back work needs reporting.
func OnlyCanceled(err error) bool {
	for err != nil {
		switch e := err.(type) {
		case *merge.PostCommitError, *journal.RollbackError:
			return false
		case interface{ Unwrap() []error }:
			causes := e.Unwrap()
			for _, cause := range causes {
				if !OnlyCanceled(cause) {
					return false
				}
			}
			return len(causes) > 0
		case interface{ Unwrap() error }:
			err = e.Unwrap()
		default:
			return errors.Is(err, context.Canceled)
		}
	}
	return false
}
