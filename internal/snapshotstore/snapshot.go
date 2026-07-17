// Package snapshotstore atomically publishes immutable database generations.
package snapshotstore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

type Candidate struct {
	LogicalPath    string
	GenerationPath string
	generationRoot string
}

// Prepare creates an unpublished generation beside logicalPath.
func Prepare(logicalPath string) (*Candidate, error) {
	logicalPath = filepath.Clean(logicalPath)
	parent, base := filepath.Dir(logicalPath), filepath.Base(logicalPath)
	generationRoot := filepath.Join(parent, "."+base+".generations")
	if err := os.MkdirAll(generationRoot, 0755); err != nil {
		return nil, fmt.Errorf("snapshot: create generation root: %w", err)
	}
	generationPath, err := os.MkdirTemp(generationRoot, "gen-")
	if err != nil {
		return nil, fmt.Errorf("snapshot: create generation: %w", err)
	}
	return &Candidate{LogicalPath: logicalPath, GenerationPath: generationPath, generationRoot: generationRoot}, nil
}

// SeedFromActive clones the active immutable generation into the candidate.
// Badger SSTables are immutable and therefore safe to hard-link; mutable logs,
// manifests, and sidecars are copied.
func (c *Candidate) SeedFromActive() error {
	source, err := filepath.EvalSymlinks(c.LogicalPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("snapshot: resolve active generation: %w", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("snapshot: read active generation: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "LOCK" {
			continue
		}
		src := filepath.Join(source, entry.Name())
		dst := filepath.Join(c.GenerationPath, entry.Name())
		if filepath.Ext(entry.Name()) == ".sst" {
			if err := os.Link(src, dst); err == nil {
				continue
			}
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("snapshot: open %s: %w", src, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("snapshot: stat %s: %w", src, err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("snapshot: create %s: %w", dst, err)
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("snapshot: copy %s: %w", src, copyErr)
	}
	return nil
}

// Publish atomically switches logicalPath to this completed generation. A
// legacy directory is exchanged atomically with the new symlink on Linux and
// retained under the generation root.
func (c *Candidate) Publish() error {
	if c == nil {
		return fmt.Errorf("snapshot: nil candidate")
	}
	if info, err := os.Stat(c.GenerationPath); err != nil || !info.IsDir() {
		return fmt.Errorf("snapshot: candidate is not a directory")
	}
	parent := filepath.Dir(c.LogicalPath)
	relativeTarget, err := filepath.Rel(parent, c.GenerationPath)
	if err != nil {
		return fmt.Errorf("snapshot: relative target: %w", err)
	}
	tempLink := filepath.Join(parent, fmt.Sprintf(".%s.next-%d", filepath.Base(c.LogicalPath), time.Now().UnixNano()))
	if err := os.Symlink(relativeTarget, tempLink); err != nil {
		return fmt.Errorf("snapshot: create publication link: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tempLink)
		}
	}()
	info, statErr := os.Lstat(c.LogicalPath)
	switch {
	case os.IsNotExist(statErr):
		err = os.Rename(tempLink, c.LogicalPath)
	case statErr != nil:
		err = statErr
	case info.Mode()&os.ModeSymlink != 0:
		err = os.Rename(tempLink, c.LogicalPath)
	case info.IsDir() && runtime.GOOS == "linux":
		err = unix.Renameat2(unix.AT_FDCWD, tempLink, unix.AT_FDCWD, c.LogicalPath, unix.RENAME_EXCHANGE)
		if err == nil {
			legacy := filepath.Join(c.generationRoot, fmt.Sprintf("legacy-%d", time.Now().UnixNano()))
			err = os.Rename(tempLink, legacy)
		}
	default:
		err = fmt.Errorf("logical path is neither a directory nor symlink")
	}
	if err != nil {
		return fmt.Errorf("snapshot: publish: %w", err)
	}
	published = true
	if dir, openErr := os.Open(parent); openErr == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	if err != nil {
		return fmt.Errorf("snapshot: sync publication: %w", err)
	}
	return nil
}

// Prune retains the active generation and the newest rollback generations up
// to keep total directories. Shared hard-linked SSTables remain valid.
func (c *Candidate) Prune(keep int) error {
	if keep < 1 {
		return fmt.Errorf("snapshot: retention must keep at least one generation")
	}
	active, err := filepath.EvalSymlinks(c.LogicalPath)
	if err != nil {
		return fmt.Errorf("snapshot: resolve active generation for pruning: %w", err)
	}
	entries, err := os.ReadDir(c.generationRoot)
	if err != nil {
		return fmt.Errorf("snapshot: read generations for pruning: %w", err)
	}
	type generation struct {
		path    string
		modTime time.Time
	}
	var generations []generation
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		generations = append(generations, generation{path: filepath.Join(c.generationRoot, entry.Name()), modTime: info.ModTime()})
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i].modTime.After(generations[j].modTime) })
	retained := map[string]bool{filepath.Clean(active): true}
	for _, generation := range generations {
		if len(retained) >= keep {
			break
		}
		retained[filepath.Clean(generation.path)] = true
	}
	for _, generation := range generations {
		if retained[filepath.Clean(generation.path)] {
			continue
		}
		if err := os.RemoveAll(generation.path); err != nil {
			return fmt.Errorf("snapshot: prune %s: %w", generation.path, err)
		}
	}
	return nil
}
