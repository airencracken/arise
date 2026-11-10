package executor

import "fmt"

// ActionError retains package identity independently of the technical wrapper.
type ActionError struct {
	Package string
	Err     error
}

func (e *ActionError) Error() string         { return fmt.Sprintf("executor: %s: %v", e.Package, e.Err) }
func (e *ActionError) Unwrap() error         { return e.Err }
func (e *ActionError) FailedPackage() string { return e.Package }
