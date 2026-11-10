package binpkg

import (
	"fmt"

	"github.com/airencracken/arise/internal/diagnostic"
)

type UnsupportedRecoverySchemaError struct{ Schema int }

func (e *UnsupportedRecoverySchemaError) Error() string {
	return fmt.Sprintf("binpkg: unsupported recovery manifest schema %d", e.Schema)
}
func (e *UnsupportedRecoverySchemaError) UserDiagnostic() diagnostic.Message {
	return diagnostic.Message{Summary: "Arise cannot read this recovery backup format.", Context: []string{fmt.Sprintf("Backup format version: %d; supported versions: 1 and %d.", e.Schema, RecoveryManifestSchema)}, NextSteps: []string{"Check which Arise version created the backup and use a compatible version to inspect or restore it."}}
}
