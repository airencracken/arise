package binpkg

import (
	"errors"
	"fmt"
	"os"
)

var ErrCaptureChanged = errors.New("installed package files changed during backup")

// Absence is evidence too: it must still hold when the archive is written.
func verifyAbsentCapturePath(root, path string) error {
	_, source, err := installedContentPath(root, path)
	if err != nil {
		return err
	}
	if err := rejectSymlinkParents(root, source); err != nil {
		return err
	}
	_, err = os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("binpkg: verify absent installed path %s: %w", path, err)
	}
	return fmt.Errorf("binpkg: installed path %s appeared while its backup was being written: %w", path, ErrCaptureChanged)
}
