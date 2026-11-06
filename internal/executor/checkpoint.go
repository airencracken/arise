package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/resolve"
)

var resumeOwners = struct {
	sync.Mutex
	paths map[string]bool
}{paths: make(map[string]bool)}

type resumeCheckpoint struct {
	path    string
	private bool
	release func() error
}

func checkResumeSnapshot(path, expected string) error {
	if expected == "" {
		return nil
	}
	_, _, actual, err := resolve.LoadResumeSnapshot(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("executor: resume checkpoint changed while planning; run arise --resume again")
	}
	return nil
}

// FinalizeResumeCheckpoint reserves ownership for completed/no-op resume
// bookkeeping, which does not enter Execute's package mutation path.
func FinalizeResumeCheckpoint(path, expected string, finish func() error) (returnErr error) {
	checkpoint, err := reserveResumeCheckpoint(path, true)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, checkpoint.release()) }()
	if err := checkResumeSnapshot(path, expected); err != nil {
		return err
	}
	return finish()
}

// Reserve a process and transaction lifetime lease, separate from the short
// read/modify/write lock used for each checkpoint update. POSIX record locks
// alone do not distinguish two executor calls in the same process.
func tryResumeLease(path string) (func() error, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolved
	} else {
		return nil, err
	}
	key := filepath.Join(parent, filepath.Base(absolute))
	resumeOwners.Lock()
	if resumeOwners.paths[key] {
		resumeOwners.Unlock()
		return nil, oplock.ErrBusy
	}
	resumeOwners.paths[key] = true
	resumeOwners.Unlock()
	forget := func() { resumeOwners.Lock(); delete(resumeOwners.paths, key); resumeOwners.Unlock() }
	lock, err := oplock.TryAcquirePath(key + ".active")
	if err != nil {
		forget()
		return nil, err
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() { releaseErr = lock.Release(); forget() })
		return releaseErr
	}, nil
}

func reserveResumeCheckpoint(path string, resuming bool) (resumeCheckpoint, error) {
	release, err := tryResumeLease(path)
	if err == nil {
		return resumeCheckpoint{path: path, release: release}, nil
	}
	if !errors.Is(err, oplock.ErrBusy) {
		return resumeCheckpoint{}, fmt.Errorf("executor: reserve resume checkpoint: %w", err)
	}
	if resuming {
		return resumeCheckpoint{}, fmt.Errorf("executor: resume checkpoint %s is still used by an active transaction; wait for it to finish", path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".concurrent-*")
	if err != nil {
		return resumeCheckpoint{}, fmt.Errorf("executor: create separate concurrent checkpoint: %w", err)
	}
	private := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(private)
		return resumeCheckpoint{}, err
	}
	release, err = tryResumeLease(private)
	if err != nil {
		_ = os.Remove(private)
		return resumeCheckpoint{}, err
	}
	return resumeCheckpoint{path: private, private: true, release: release}, nil
}
