//go:build linux

package lifecycletrace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ProcResolver resolves tracee-relative syscall paths using procfs handles to
// the tracee's actual cwd and dirfds.
type ProcResolver struct{}

func (ProcResolver) ResolveFD(pid int, fd int) (string, bool, error) {
	if fd < 0 {
		return "", false, fmt.Errorf("invalid fd %d", fd)
	}
	resolved, err := os.Readlink(filepath.Join("/proc", fmt.Sprintf("%d", pid), "fd", fmt.Sprintf("%d", fd)))
	if err != nil {
		return "", false, err
	}
	// Pipes, sockets and anon-inodes have descriptive non-path targets. Deleted
	// files and memfds have no reachable directory entry to restore.
	if !filepath.IsAbs(resolved) {
		return "", false, nil
	}
	if strings.HasSuffix(resolved, " (deleted)") || strings.HasPrefix(resolved, "/memfd:") {
		return "", false, nil
	}
	info, err := os.Stat(filepath.Join("/proc", fmt.Sprintf("%d", pid), "fd", fmt.Sprintf("%d", fd)))
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return "", false, nil
	}
	return filepath.Clean(resolved), true, nil
}

func (ProcResolver) Resolve(pid int, dirfd int, path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	base := filepath.Join("/proc", fmt.Sprintf("%d", pid), "cwd")
	if dirfd != unix.AT_FDCWD {
		if dirfd < 0 {
			return "", fmt.Errorf("invalid dirfd %d", dirfd)
		}
		base = filepath.Join("/proc", fmt.Sprintf("%d", pid), "fd", fmt.Sprintf("%d", dirfd))
	}
	resolvedBase, err := os.Readlink(base)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(resolvedBase) {
		return "", fmt.Errorf("procfs target is not absolute: %q", resolvedBase)
	}
	return filepath.Clean(filepath.Join(resolvedBase, path)), nil
}
