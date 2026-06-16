package merge

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const envSuffix = ".environment"

// MergeConfig holds the target paths for a merge operation.
type MergeConfig struct {
	RootDir  string
	VdbDir   string
	Category string
	Package  string
	Version  string
}

// VdbPath returns the VDB entry path for the merge config.
func (c MergeConfig) VdbPath() string {
	return filepath.Join(c.VdbDir, c.Category, c.Package+"-"+c.Version)
}

func (c MergeConfig) vdbPath() string {
	return c.VdbPath()
}

// Merge walks the destDir and installs every entry into the root filesystem
// under cfg.RootDir, then writes a VDB CONTENTS record and an environment file.
func Merge(ctx context.Context, destDir string, cfg MergeConfig) error {
	vdbDir := cfg.vdbPath()
	if err := os.MkdirAll(vdbDir, 0755); err != nil {
		return fmt.Errorf("merge: could not create package database directory %s: %w", vdbDir, err)
	}

	var lines []string

	err := filepath.WalkDir(destDir, func(srcPath string, d os.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(destDir, srcPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		targetPath := filepath.Join(cfg.RootDir, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case d.IsDir():
			if err := os.MkdirAll(targetPath, info.Mode()); err != nil {
				return fmt.Errorf("merge: could not create directory %s: %w", targetPath, err)
			}
			lines = append(lines, formatContentsDir(targetPath))
			return nil

		case d.Type()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("merge: could not read symlink %s: %w", srcPath, err)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("merge: could not create parent directory for symlink %s: %w", targetPath, err)
			}
			if err := os.Symlink(linkTarget, targetPath); err != nil {
				return fmt.Errorf("merge: could not create symlink %s: %w", targetPath, err)
			}
			md5sum, _ := md5Bytes([]byte(linkTarget))
			lines = append(lines, formatContentsSym(targetPath, linkTarget, md5sum, info.ModTime().Unix()))
			return nil

		default:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("merge: could not create parent directory for %s: %w", targetPath, err)
			}
			md5sum, err := copyFile(srcPath, targetPath, info.Mode())
			if err != nil {
				return fmt.Errorf("merge: could not copy %s into the filesystem: %w", srcPath, err)
			}
			lines = append(lines, formatContentsObj(targetPath, md5sum, info.ModTime().Unix()))
			return nil
		}
	})

	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(vdbDir, "CONTENTS"), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("merge: could not write package file list: %w", err)
	}

	envContent := fmt.Sprintf("MERGE_DATE=%d\n", time.Now().Unix())
	if err := os.WriteFile(filepath.Join(vdbDir, envSuffix), []byte(envContent), 0644); err != nil {
		return fmt.Errorf("merge: could not write package environment data: %w", err)
	}

	return nil
}

// Unmerge reads the VDB CONTENTS file for the package at pkgPath and removes
// every listed file from the root filesystem. It then removes empty parent
// directories and deletes the VDB entry itself.
func Unmerge(ctx context.Context, pkgPath string) error {
	contentsPath := filepath.Join(pkgPath, "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("unmerge: no file list found for package at %s — it may already be removed", pkgPath)
		}
		return fmt.Errorf("unmerge: could not read the file list for removal: %w", err)
	}

	entries, err := parseContents(string(data))
	if err != nil {
		return fmt.Errorf("unmerge: could not parse the file list for removal: %w", err)
	}

	var filePaths []string
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		filePaths = append(filePaths, e.Path)
	}

	for i := len(filePaths) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		path := filePaths[i]
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("unmerge: could not check file %s: %w", path, err)
		}
		if info.IsDir() {
			if isEmptyDir(path) {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("unmerge: could not remove empty directory %s: %w", path, err)
				}
			}
			continue
		}
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("unmerge: could not remove file %s: %w", path, err)
			}
		}
	}

	if err := removeEmptyParents(filePaths); err != nil {
		return fmt.Errorf("unmerge: could not clean up empty parent directories: %w", err)
	}

	if err := os.Remove(contentsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unmerge: could not remove the file list: %w", err)
	}

	envPath := filepath.Join(pkgPath, envSuffix)
	if err := os.Remove(envPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unmerge: could not remove the environment file: %w", err)
	}

	if err := os.Remove(pkgPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unmerge: could not remove the package database directory: %w", err)
	}

	return nil
}

type contentsEntry struct {
	Type string // "obj", "dir", "sym"
	Path string
	MD5  string
	Mtime int64
}

func parseContents(text string) ([]contentsEntry, error) {
	var entries []contentsEntry
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		e := contentsEntry{Type: fields[0], Path: fields[1]}
		switch e.Type {
		case "obj":
			if len(fields) >= 4 {
				e.MD5 = fields[2]
				e.Mtime, _ = strconv.ParseInt(fields[3], 10, 64)
			}
		case "sym":
			if len(fields) >= 4 {
				e.MD5 = fields[2]
				e.Mtime, _ = strconv.ParseInt(fields[3], 10, 64)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func formatContentsObj(path, md5sum string, mtime int64) string {
	return fmt.Sprintf("obj %s %s %d", path, md5sum, mtime)
}

func formatContentsDir(path string) string {
	return fmt.Sprintf("dir %s", path)
}

func formatContentsSym(path, target, md5sum string, mtime int64) string {
	return fmt.Sprintf("sym %s -> %s %s %d", path, target, md5sum, mtime)
}

func copyFile(src, dst string, mode os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() { if cerr := in.Close(); cerr != nil { /* Best effort */ } }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return "", err
	}
	defer func() { if cerr := out.Close(); cerr != nil { /* Best effort */ } }()

	h := md5.New()
	w := io.MultiWriter(out, h)
	if _, err := io.Copy(w, in); err != nil {
		return "", err
	}

	md5sum := hex.EncodeToString(h.Sum(nil))
	return md5sum, nil
}

func md5Bytes(data []byte) (string, error) {
	h := md5.New()
	if _, err := h.Write(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isEmptyDir(path string) bool {
	dh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { if cerr := dh.Close(); cerr != nil { /* Best effort */ } }()
	names, err := dh.Readdirnames(1)
	return err == io.EOF && len(names) == 0
}

func removeEmptyParents(paths []string) error {
	parents := make(map[string]bool)
	for _, p := range paths {
		dir := filepath.Dir(p)
		for dir != "/" && dir != "." {
			parents[dir] = true
			dir = filepath.Dir(dir)
		}
	}

	var ordered []string
	for p := range parents {
		ordered = append(ordered, p)
	}
	sortByDepth(ordered)

	for _, p := range ordered {
		if isEmptyDir(p) {
			if err := os.Remove(p); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
			}
		}
	}
	return nil
}

func sortByDepth(dirs []string) {
	for i := 0; i < len(dirs)-1; i++ {
		for j := i + 1; j < len(dirs); j++ {
			if depth(dirs[i]) < depth(dirs[j]) {
				dirs[i], dirs[j] = dirs[j], dirs[i]
			}
		}
	}
}

func depth(p string) int {
	return strings.Count(filepath.Clean(p), string(os.PathSeparator))
}
