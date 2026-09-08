package merge

import (
	"os"
	"strings"

	"github.com/airencracken/arise/internal/journal"
)

// removeRecordedPath removes only the object recorded at installation time.
// Modified payloads and objects that changed type belong to the user now.
// This rule applies to uninstall and to obsolete files during replacement.
func removeRecordedPath(operation *journal.Journal, path string, entry contentsEntry) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	matches, err := recordedObjectMatches(path, entry, info)
	if err != nil || !matches {
		return err
	}
	if info.IsDir() && !isEmptyDir(path) {
		return nil
	}
	if err := operation.Capture(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func recordedObjectMatches(path string, entry contentsEntry, info os.FileInfo) (bool, error) {
	switch entry.Type {
	case "dir":
		return info.IsDir(), nil
	case "obj":
		if !info.Mode().IsRegular() || info.ModTime().Unix() != entry.Mtime {
			return false, nil
		}
		digest, err := md5File(path)
		return strings.EqualFold(digest, entry.MD5), err
	case "sym":
		if info.Mode()&os.ModeSymlink == 0 || info.ModTime().Unix() != entry.Mtime {
			return false, nil
		}
		target, err := os.Readlink(path)
		return target == entry.LinkTarget, err
	default:
		// Named pipes and device nodes can retain runtime state; leave them alone.
		return false, nil
	}
}
