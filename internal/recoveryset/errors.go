package recoveryset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/diagnostic"
)

type CaptureError struct {
	VDBEntryPath string
	Directory    string
	Err          error
}

func (e *CaptureError) Error() string {
	return fmt.Sprintf("recovery set: capture %s: %v", e.VDBEntryPath, e.Err)
}
func (e *CaptureError) Unwrap() error { return e.Err }

func (e *CaptureError) UserDiagnostic() diagnostic.Message {
	cpv := filepath.Base(filepath.Dir(e.VDBEntryPath)) + "/" + filepath.Base(e.VDBEntryPath)
	message := diagnostic.Message{
		Summary:   "Could not save a rollback backup for " + cpv + ".",
		Context:   []string{"The update backs up installed packages before replacing them.", fmt.Sprintf("Backup directory: %q", e.Directory)},
		NextSteps: []string{"Check the installed package record and the cause below before retrying."},
	}
	var pathError *os.PathError
	if errors.As(e.Err, &pathError) {
		message.Context = append(message.Context, fmt.Sprintf("Affected path: %q", pathError.Path))
		message.Details = []string{pathError.Err.Error()}
	} else {
		message.Context = append(message.Context, fmt.Sprintf("Installed package record: %q", e.VDBEntryPath))
		message.Details = []string{e.Err.Error()}
	}
	switch {
	case errors.Is(e.Err, syscall.ENOSPC), errors.Is(e.Err, syscall.EDQUOT):
		message.NextSteps = []string{"Free space or quota in the backup directory, or choose another --binpkg-dir, then rerun the original command."}
	case errors.Is(e.Err, os.ErrPermission):
		message.NextSteps = []string{"Check access permissions for the affected path, then rerun the original command."}
	case errors.Is(e.Err, syscall.EROFS):
		message.NextSteps = []string{"Use a writable backup location with --binpkg-dir, then rerun the original command."}
	case errors.Is(e.Err, os.ErrNotExist), errors.Is(e.Err, binpkg.ErrCaptureChanged):
		message.NextSteps = []string{"The package files changed during capture. Let other operations affecting this package finish, then rerun the original command."}
	}
	return message
}
