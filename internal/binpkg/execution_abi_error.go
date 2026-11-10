package binpkg

import (
	"fmt"

	"github.com/airencracken/arise/internal/diagnostic"
	"github.com/airencracken/arise/internal/portage"
)

type IncompatibleExecutionABIError struct{ ABI string }

func (e *IncompatibleExecutionABIError) Error() string {
	return fmt.Sprintf("binpkg: Arise execution ABI %s requires rebuilding for execution ABI %s", e.ABI, portage.PhaseEnvironmentABI)
}

func (e *IncompatibleExecutionABIError) UserDiagnostic() diagnostic.Message {
	return diagnostic.Message{Summary: "This binary package was built with an incompatible version of Arise.", NextSteps: []string{"Rebuild the package from source. If --usepkgonly was selected, rerun without it to allow a source build."}}
}
