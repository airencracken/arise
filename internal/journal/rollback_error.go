package journal

import (
	"fmt"

	"github.com/airencracken/arise/internal/diagnostic"
)

type RollbackError struct {
	Operation string
	Directory string
	Cause     error
	Err       error
}

func (e *RollbackError) Error() string {
	return fmt.Sprintf("%v; %s: rollback journal %s: %v", e.Cause, e.Operation, e.Directory, e.Err)
}
func (e *RollbackError) Unwrap() []error { return []error{e.Cause, e.Err} }
func (e *RollbackError) UserDiagnostic() diagnostic.Message {
	return diagnostic.Message{
		Summary:   "The package operation failed, and automatic rollback could not finish.",
		Context:   []string{fmt.Sprintf("Recovery journal: %q", e.Directory)},
		Details:   []string{e.Cause.Error(), e.Err.Error()},
		NextSteps: []string{"Run 'arise recover status' to inspect interrupted operations. Resolve the reported cause before applying recovery."},
	}
}
