// Package oplock coordinates package-state mutations with Portage.
package oplock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/gentooling"
	"golang.org/x/sys/unix"
)

type Lock struct {
	file *os.File
	path string
}

// PortageLockPath mirrors portage.locks.lockdir: a directory lock is stored in
// its parent as .<basename>.portage_lockfile.
func PortageLockPath(directory string) string {
	return gentooling.PortageStateLockPath(directory)
}

// TryAcquireVDB acquires Portage's VDB mutation lock without waiting.
func TryAcquireVDB(vdbDirectory string) (*Lock, error) {
	return tryAcquire(PortageLockPath(vdbDirectory))
}

// AcquireVDB waits for Portage's VDB mutation lock. Live package transactions
// use this so independent package-manager processes queue instead of failing
// merely because another transaction is currently committing package state.
func AcquireVDB(vdbDirectory string) (*Lock, error) {
	return acquire(PortageLockPath(vdbDirectory))
}

// TryAcquirePath acquires Portage's sibling lock for a mutable state path,
// such as /var/lib/portage/world.
func TryAcquirePath(path string) (*Lock, error) {
	return tryAcquire(PortageLockPath(path))
}

// AcquirePath waits for the sibling lock protecting a mutable state path.
// Callers that perform read-modify-write updates should use this instead of
// TryAcquirePath so concurrent writers cannot lose one another's changes.
func AcquirePath(path string) (*Lock, error) {
	return acquire(PortageLockPath(path))
}

func acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("operation lock: create parent: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return nil, fmt.Errorf("operation lock: open %s: %w", path, err)
	}
	flock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := unix.FcntlFlock(file.Fd(), unix.F_SETLKW, &flock); err != nil {
		file.Close()
		return nil, fmt.Errorf("operation lock: acquire %s: %w", path, err)
	}
	return &Lock{file: file, path: path}, nil
}

func tryAcquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("operation lock: create parent: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return nil, fmt.Errorf("operation lock: open %s: %w", path, err)
	}
	flock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &flock); err != nil {
		file.Close()
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("operation lock: Portage VDB is busy (%s): %w", path, err)
		}
		return nil, fmt.Errorf("operation lock: acquire %s: %w", path, err)
	}
	return &Lock{file: file, path: path}, nil
}

func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	flock := unix.Flock_t{Type: unix.F_UNLCK, Whence: 0, Start: 0, Len: 0}
	err := unix.FcntlFlock(l.file.Fd(), unix.F_SETLK, &flock)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("operation lock: unlock %s: %w", l.path, err)
	}
	if closeErr != nil {
		return fmt.Errorf("operation lock: close %s: %w", l.path, closeErr)
	}
	return nil
}
