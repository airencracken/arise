package merge

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/dsnet/compress/bzip2"
	"golang.org/x/sys/unix"
)

const envSuffix = ".environment"

// MergeConfig holds the target paths for a merge operation.
type MergeConfig struct {
	RootDir                  string
	VdbDir                   string
	Category                 string
	Package                  string
	Version                  string
	JournalDir               string            // non-empty enables durable rollback journaling
	AllowLiveRoot            bool              // requires the explicit live-root journal entry point
	AllowLiveReplacement     bool              // exact same-version, lifecycle-free canary only
	VDBLockHeld              bool              // caller owns the operation-wide VDB lock
	ReplacedVDBPath          string            // optional old version entry removed in the same transaction
	VDBMetadata              map[string]string // validated Portage-readable metadata files
	BeforeReplacementRemoval func() error
	AfterReplacementRemoval  func() error
	BeforeCommit             func() error
	ConfigProtect            []string
	ConfigProtectMask        []string
	Environment              []byte // normalized package environment snapshot
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
func Merge(ctx context.Context, destDir string, cfg MergeConfig) (returnErr error) {
	if filepath.Clean(cfg.RootDir) == string(filepath.Separator) && cfg.AllowLiveRoot {
		if _, err := os.Lstat(cfg.vdbPath()); err == nil && !cfg.AllowLiveReplacement {
			return fmt.Errorf("merge: live new-install canary refuses existing VDB entry %s", cfg.vdbPath())
		} else if !os.IsNotExist(err) {
			if err != nil {
				return fmt.Errorf("merge: inspect live canary VDB entry: %w", err)
			}
		}
		if cfg.AllowLiveReplacement {
			replacedVDB := cfg.ReplacedVDBPath
			if replacedVDB == "" {
				replacedVDB = cfg.vdbPath()
			}
			if err := validateLiveReplacementTargets(destDir, cfg.RootDir, replacedVDB); err != nil {
				return err
			}
		} else if err := validateLiveNewInstallTargets(destDir, cfg.RootDir); err != nil {
			return err
		}
	}
	if !cfg.VDBLockHeld {
		lock, err := oplock.TryAcquireVDB(cfg.VdbDir)
		if err != nil {
			return fmt.Errorf("merge: %w", err)
		}
		defer func() {
			if releaseErr := lock.Release(); releaseErr != nil && returnErr == nil {
				returnErr = fmt.Errorf("merge: %w", releaseErr)
			}
		}()
	}
	collisions, err := CheckCollisions(destDir, cfg.VdbDir, []string{cfg.Category + "/" + cfg.Package})
	if err != nil {
		return fmt.Errorf("merge: ownership preflight: %w", err)
	}
	if len(collisions) != 0 {
		return fmt.Errorf("merge: ownership preflight failed: %s", strings.Join(collisions, "; "))
	}
	if cfg.JournalDir == "" {
		return merge(ctx, destDir, cfg, nil)
	}
	if _, err := journal.RecoverActive(cfg.JournalDir); err != nil {
		return fmt.Errorf("merge: recover interrupted journal: %w", err)
	}
	var j *journal.Journal
	if filepath.Clean(cfg.RootDir) == string(filepath.Separator) && cfg.AllowLiveRoot {
		j, err = journal.BeginLiveRoot(cfg.JournalDir)
	} else {
		j, err = journal.Begin(cfg.JournalDir, cfg.RootDir)
	}
	if err != nil {
		return fmt.Errorf("merge: begin journal: %w", err)
	}
	if err := merge(ctx, destDir, cfg, j); err != nil {
		if rollbackErr := j.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%v; merge: rollback journal %s: %w", err, j.Dir(), rollbackErr)
		}
		return fmt.Errorf("%w (rolled back via %s)", err, j.Dir())
	}
	if err := j.Commit(); err != nil {
		if rollbackErr := j.Rollback(); rollbackErr != nil {
			return fmt.Errorf("merge: commit journal %s: %v; rollback: %w", j.Dir(), err, rollbackErr)
		}
		return fmt.Errorf("merge: commit journal %s: %w", j.Dir(), err)
	}
	return nil
}

func validateLiveReplacementTargets(destDir, rootDir, vdbPath string) error {
	data, err := os.ReadFile(filepath.Join(vdbPath, "CONTENTS"))
	if err != nil {
		return fmt.Errorf("merge: read live replacement ownership: %w", err)
	}
	entries, err := parseContents(string(data))
	if err != nil {
		return fmt.Errorf("merge: parse live replacement ownership: %w", err)
	}
	owned := make(map[string]bool, len(entries))
	for _, entry := range entries {
		owned[filepath.Clean(entry.Path)] = true
	}
	return filepath.WalkDir(destDir, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(destDir, source)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(rootDir, relative)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("merge: inspect live replacement target %s: %w", target, err)
		}
		if entry.IsDir() && info.IsDir() {
			return nil
		}
		canonical := string(filepath.Separator) + filepath.ToSlash(relative)
		if !owned[filepath.Clean(canonical)] {
			return fmt.Errorf("merge: live replacement target is not owned by replaced package: %s", target)
		}
		return nil
	})
}

// validateLiveNewInstallTargets limits the first live lane to additive package
// state. Existing directories may be shared, but no file, symlink or other
// object may be replaced, whether VDB-owned or local/unowned.
func validateLiveNewInstallTargets(destDir, rootDir string) error {
	return filepath.WalkDir(destDir, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(destDir, source)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(rootDir, relative)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("merge: inspect live canary target %s: %w", target, err)
		}
		if entry.IsDir() && info.IsDir() {
			return nil
		}
		return fmt.Errorf("merge: live new-install canary refuses existing target %s", target)
	})
}

func merge(ctx context.Context, destDir string, cfg MergeConfig, operation *journal.Journal) error {
	mergeTime := time.Now().Unix()
	vdbDir := cfg.vdbPath()
	if operation != nil {
		if _, err := os.Lstat(vdbDir); err == nil {
			if err := operation.RemoveTree(vdbDir); err != nil {
				return fmt.Errorf("merge: journal existing package database directory: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("merge: inspect package database directory %s: %w", vdbDir, err)
		}
		if err := operation.Capture(vdbDir); err != nil {
			return fmt.Errorf("merge: journal package database directory: %w", err)
		}
	}
	if err := os.MkdirAll(vdbDir, 0755); err != nil {
		return fmt.Errorf("merge: could not create package database directory %s: %w", vdbDir, err)
	}

	var lines []string
	type installedHardlink struct {
		path string
		md5  string
	}
	hardlinks := make(map[[2]uint64]installedHardlink)
	type createdDirectory struct {
		path string
		info os.FileInfo
	}
	var createdDirectories []createdDirectory

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
			targetInfo, inspectErr := os.Lstat(targetPath)
			created := os.IsNotExist(inspectErr)
			if inspectErr != nil && !created {
				return fmt.Errorf("merge: inspect target directory %s: %w", targetPath, inspectErr)
			}
			if !created && !targetInfo.IsDir() {
				if err := replaceNonDirectory(operation, targetPath); err != nil {
					return fmt.Errorf("merge: replace non-directory target %s: %w", targetPath, err)
				}
				created = true
			} else if operation != nil {
				if err := operation.Capture(targetPath); err != nil {
					return fmt.Errorf("merge: journal target %s: %w", targetPath, err)
				}
			}
			if err := os.MkdirAll(targetPath, info.Mode()); err != nil {
				return fmt.Errorf("merge: could not create directory %s: %w", targetPath, err)
			}
			if created {
				createdDirectories = append(createdDirectories, createdDirectory{path: targetPath, info: info})
			}
			lines = append(lines, formatContentsDir(contentsPathForRoot(cfg.RootDir, targetPath)))
			return nil

		case d.Type()&os.ModeSymlink != 0:
			if err := prepareNonDirectoryTarget(operation, targetPath); err != nil {
				return fmt.Errorf("merge: prepare symlink target %s: %w", targetPath, err)
			}
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
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				if err := os.Lchown(targetPath, int(stat.Uid), int(stat.Gid)); err != nil {
					return fmt.Errorf("merge: preserve symlink ownership %s: %w", targetPath, err)
				}
			}
			if err := copyXattrs(srcPath, targetPath, true); err != nil {
				return fmt.Errorf("merge: preserve symlink xattrs %s: %w", targetPath, err)
			}
			times := []unix.Timespec{unix.NsecToTimespec(info.ModTime().UnixNano()), unix.NsecToTimespec(info.ModTime().UnixNano())}
			if err := unix.UtimesNanoAt(unix.AT_FDCWD, targetPath, times, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return fmt.Errorf("merge: preserve symlink timestamp %s: %w", targetPath, err)
			}
			md5sum, _ := md5Bytes([]byte(linkTarget))
			lines = append(lines, formatContentsSym(contentsPathForRoot(cfg.RootDir, targetPath), linkTarget, md5sum, info.ModTime().Unix()))
			return nil

		default:
			if protectedPath(rel, cfg.ConfigProtect, cfg.ConfigProtectMask) {
				if _, statErr := os.Lstat(targetPath); statErr == nil {
					same, compareErr := sameRegularFile(srcPath, targetPath)
					if compareErr != nil {
						return compareErr
					}
					if !same {
						targetPath, err = nextProtectedPath(targetPath)
						if err != nil {
							return err
						}
					}
				} else if !os.IsNotExist(statErr) {
					return statErr
				}
			}
			if err := prepareNonDirectoryTarget(operation, targetPath); err != nil {
				return fmt.Errorf("merge: prepare regular-file target %s: %w", targetPath, err)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("merge: could not create parent directory for %s: %w", targetPath, err)
			}
			var md5sum string
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
				key := [2]uint64{uint64(stat.Dev), stat.Ino}
				if installed, exists := hardlinks[key]; exists {
					if err := os.Link(installed.path, targetPath); err != nil {
						return fmt.Errorf("merge: preserve hardlink %s -> %s: %w", targetPath, installed.path, err)
					}
					md5sum = installed.md5
				} else {
					md5sum, err = copyFile(srcPath, targetPath, info.Mode(), info.ModTime(), stat)
					if err == nil {
						hardlinks[key] = installedHardlink{path: targetPath, md5: md5sum}
					}
				}
			} else {
				stat, _ := info.Sys().(*syscall.Stat_t)
				md5sum, err = copyFile(srcPath, targetPath, info.Mode(), info.ModTime(), stat)
			}
			if err != nil {
				return fmt.Errorf("merge: could not copy %s into the filesystem: %w", srcPath, err)
			}
			lines = append(lines, formatContentsObj(contentsPathForRoot(cfg.RootDir, targetPath), md5sum, info.ModTime().Unix()))
			return nil
		}
	})

	if err != nil {
		return err
	}
	for index := len(createdDirectories) - 1; index >= 0; index-- {
		directory := createdDirectories[index]
		if stat, ok := directory.info.Sys().(*syscall.Stat_t); ok {
			if err := os.Chown(directory.path, int(stat.Uid), int(stat.Gid)); err != nil {
				return fmt.Errorf("merge: preserve directory ownership %s: %w", directory.path, err)
			}
		}
		mode := directory.info.Mode()
		if err := os.Chmod(directory.path, mode.Perm()|mode&os.ModeSetgid|mode&os.ModeSticky); err != nil {
			return fmt.Errorf("merge: preserve directory mode %s: %w", directory.path, err)
		}
		sourceDirectory := filepath.Join(destDir, strings.TrimPrefix(contentsPathForRoot(cfg.RootDir, directory.path), string(filepath.Separator)))
		if err := copyXattrs(sourceDirectory, directory.path, false); err != nil {
			return fmt.Errorf("merge: preserve directory xattrs %s: %w", directory.path, err)
		}
		if err := os.Chtimes(directory.path, directory.info.ModTime(), directory.info.ModTime()); err != nil {
			return fmt.Errorf("merge: preserve directory timestamp %s: %w", directory.path, err)
		}
	}

	contentsPath := filepath.Join(vdbDir, "CONTENTS")
	if operation != nil {
		if err := operation.Capture(contentsPath); err != nil {
			return fmt.Errorf("merge: journal package file list: %w", err)
		}
	}
	if err := os.WriteFile(contentsPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("merge: could not write package file list: %w", err)
	}

	envContent := fmt.Sprintf("MERGE_DATE=%d\n", mergeTime)
	envPath := filepath.Join(vdbDir, envSuffix)
	if operation != nil {
		if err := operation.Capture(envPath); err != nil {
			return fmt.Errorf("merge: journal package environment: %w", err)
		}
	}
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		return fmt.Errorf("merge: could not write package environment data: %w", err)
	}
	if len(cfg.Environment) != 0 {
		compressedPath := filepath.Join(vdbDir, "environment.bz2")
		if operation != nil {
			if err := operation.Capture(compressedPath); err != nil {
				return fmt.Errorf("merge: journal compressed package environment: %w", err)
			}
		}
		output, err := os.OpenFile(compressedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("merge: create compressed package environment: %w", err)
		}
		compressor, err := bzip2.NewWriter(output, nil)
		if err != nil {
			output.Close()
			return fmt.Errorf("merge: initialize compressed package environment: %w", err)
		}
		if _, err := compressor.Write(cfg.Environment); err != nil {
			compressor.Close()
			output.Close()
			return fmt.Errorf("merge: compress package environment: %w", err)
		}
		if err := compressor.Close(); err != nil {
			output.Close()
			return fmt.Errorf("merge: finalize compressed package environment: %w", err)
		}
		if err := output.Sync(); err != nil {
			output.Close()
			return fmt.Errorf("merge: sync compressed package environment: %w", err)
		}
		if err := output.Close(); err != nil {
			return fmt.Errorf("merge: close compressed package environment: %w", err)
		}
	}
	metadata := make(map[string]string, len(cfg.VDBMetadata)+2)
	for name, value := range cfg.VDBMetadata {
		metadata[name] = value
	}
	needed, neededELF2, err := nativeNeededMetadata(destDir)
	if err != nil {
		return fmt.Errorf("merge: generate native ELF dependency metadata: %w", err)
	}
	if needed != "" {
		metadata["NEEDED"] = needed
		metadata["NEEDED.ELF.2"] = neededELF2
	}
	if _, exists := metadata["BUILD_TIME"]; !exists {
		metadata["BUILD_TIME"] = strconv.FormatInt(mergeTime, 10)
	}
	if _, exists := metadata["COUNTER"]; !exists {
		counter, err := allocateVDBCounter(operation, cfg.RootDir)
		if err != nil {
			return fmt.Errorf("merge: allocate VDB counter: %w", err)
		}
		metadata["COUNTER"] = strconv.FormatInt(counter, 10)
	}
	metadataNames := make([]string, 0, len(metadata))
	for name := range metadata {
		if !validVDBMetadataName(name) {
			return fmt.Errorf("merge: unsafe VDB metadata name %q", name)
		}
		metadataNames = append(metadataNames, name)
	}
	sort.Strings(metadataNames)
	for _, name := range metadataNames {
		path := filepath.Join(vdbDir, name)
		if operation != nil {
			if err := operation.Capture(path); err != nil {
				return fmt.Errorf("merge: journal VDB metadata %s: %w", name, err)
			}
		}
		value := metadata[name]
		if !strings.HasSuffix(value, "\n") {
			value += "\n"
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			return fmt.Errorf("merge: write VDB metadata %s: %w", name, err)
		}
	}
	if operation != nil && cfg.ReplacedVDBPath != "" && filepath.Clean(cfg.ReplacedVDBPath) != filepath.Clean(vdbDir) {
		if cfg.BeforeReplacementRemoval != nil {
			if err := cfg.BeforeReplacementRemoval(); err != nil {
				return fmt.Errorf("merge: replacement pre-removal lifecycle: %w", err)
			}
		}
		if err := removeObsoleteReplacementPayload(operation, destDir, vdbDir, cfg.ReplacedVDBPath, cfg); err != nil {
			return err
		}
		if err := operation.RemoveTree(cfg.ReplacedVDBPath); err != nil {
			return fmt.Errorf("merge: remove replaced package database entry: %w", err)
		}
		if cfg.AfterReplacementRemoval != nil {
			if err := cfg.AfterReplacementRemoval(); err != nil {
				return fmt.Errorf("merge: replacement post-removal lifecycle: %w", err)
			}
		}
	}
	if operation != nil && cfg.BeforeCommit != nil {
		if err := cfg.BeforeCommit(); err != nil {
			return fmt.Errorf("merge: pre-commit lifecycle: %w", err)
		}
	}

	return nil
}

func prepareNonDirectoryTarget(operation *journal.Journal, target string) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		if operation != nil {
			return operation.Capture(target)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() && !isEmptyDir(target) {
		return fmt.Errorf("refusing to replace non-empty local directory")
	}
	if operation != nil {
		return operation.RemoveTree(target)
	}
	return os.Remove(target)
}

func replaceNonDirectory(operation *journal.Journal, target string) error {
	if operation != nil {
		return operation.RemoveTree(target)
	}
	return os.Remove(target)
}

func protectedPath(relative string, protect, mask []string) bool {
	path := filepath.Join(string(filepath.Separator), relative)
	matches := func(entries []string) bool {
		for _, entry := range entries {
			entry = filepath.Clean(filepath.Join(string(filepath.Separator), strings.TrimSpace(entry)))
			if path == entry || strings.HasPrefix(path, entry+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	return matches(protect) && !matches(mask)
}

func nextProtectedPath(target string) (string, error) {
	directory, base := filepath.Dir(target), filepath.Base(target)
	for index := 0; index <= 9999; index++ {
		candidate := filepath.Join(directory, fmt.Sprintf("._cfg%04d_%s", index, base))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("merge: no CONFIG_PROTECT update name available for %s", target)
}

func sameRegularFile(left, right string) (bool, error) {
	a, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	return string(a) == string(b), nil
}

func validVDBMetadataName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	for _, r := range name {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '+' || r == '-') {
			return false
		}
	}
	return true
}

func allocateVDBCounter(operation *journal.Journal, root string) (int64, error) {
	counterPath := filepath.Join(root, "var", "cache", "edb", "counter")
	current := int64(0)
	data, err := os.ReadFile(counterPath)
	if err == nil {
		value := strings.TrimSpace(string(data))
		current, err = strconv.ParseInt(value, 10, 64)
		if err != nil || current < 0 {
			return 0, fmt.Errorf("invalid counter %q", value)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if err := ensureJournaledParent(operation, root, counterPath); err != nil {
		return 0, err
	}
	if operation != nil {
		if err := operation.Capture(counterPath); err != nil {
			return 0, err
		}
	}
	next := current + 1
	if err := os.WriteFile(counterPath, []byte(strconv.FormatInt(next, 10)), 0o644); err != nil {
		return 0, err
	}
	return next, nil
}

func ensureJournaledParent(operation *journal.Journal, root, target string) error {
	parent := filepath.Dir(target)
	var missing []string
	for path := parent; path != filepath.Clean(root); path = filepath.Dir(path) {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			missing = append(missing, path)
			continue
		} else if err != nil {
			return err
		}
		break
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if operation != nil {
			if err := operation.Capture(missing[index]); err != nil {
				return err
			}
		}
		if err := os.Mkdir(missing[index], 0o755); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func removeObsoleteReplacementPayload(operation *journal.Journal, destDir, newVDB, oldVDB string, cfg MergeConfig) error {
	data, err := os.ReadFile(filepath.Join(oldVDB, "CONTENTS"))
	if err != nil {
		return fmt.Errorf("merge: read replaced CONTENTS: %w", err)
	}
	entries, err := parseContents(string(data))
	if err != nil {
		return fmt.Errorf("merge: parse replaced CONTENTS: %w", err)
	}
	newPaths, err := gatherDestFiles(destDir)
	if err != nil {
		return err
	}
	retained := make(map[string]bool, len(newPaths))
	for _, path := range newPaths {
		retained[filepath.Clean(path)] = true
	}
	otherOwners, err := ownershipExcluding(cfg.VdbDir, oldVDB, newVDB)
	if err != nil {
		return fmt.Errorf("merge: scan replacement ownership: %w", err)
	}
	sort.SliceStable(entries, func(i, k int) bool {
		return strings.Count(filepath.Clean(entries[i].Path), string(filepath.Separator)) > strings.Count(filepath.Clean(entries[k].Path), string(filepath.Separator))
	})
	for _, entry := range entries {
		canonical, target, err := replacementPath(cfg.RootDir, entry.Path)
		if err != nil {
			return err
		}
		if retained[canonical] || otherOwners[canonical] {
			continue
		}
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := operation.Capture(target); err != nil {
			return fmt.Errorf("merge: journal obsolete replacement path %s: %w", target, err)
		}
		if entry.Type == "dir" {
			if isEmptyDir(target) {
				if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			continue
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func replacementPath(root, recorded string) (string, string, error) {
	if !filepath.IsAbs(recorded) {
		return "", "", fmt.Errorf("merge: unsafe relative CONTENTS path %q", recorded)
	}
	cleaned := filepath.Clean(recorded)
	if relative, err := filepath.Rel(filepath.Clean(root), cleaned); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Join(string(filepath.Separator), relative), cleaned, nil
	}
	canonical := filepath.Join(string(filepath.Separator), strings.TrimPrefix(cleaned, string(filepath.Separator)))
	return canonical, filepath.Join(root, strings.TrimPrefix(canonical, string(filepath.Separator))), nil
}

func contentsPathForRoot(root, target string) string {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Clean(target)
	}
	return filepath.Join(string(filepath.Separator), relative)
}

func ownershipExcluding(vdbRoot string, excluded ...string) (map[string]bool, error) {
	exclude := make(map[string]bool, len(excluded))
	for _, path := range excluded {
		exclude[filepath.Clean(path)] = true
	}
	owned := make(map[string]bool)
	err := filepath.WalkDir(vdbRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "CONTENTS" || exclude[filepath.Clean(filepath.Dir(path))] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries, err := parseContents(string(data))
		if err != nil {
			return err
		}
		for _, item := range entries {
			if item.Type != "dir" && filepath.IsAbs(item.Path) {
				owned[filepath.Clean(item.Path)] = true
			}
		}
		return nil
	})
	return owned, err
}

// Unmerge is retained for source compatibility, but removal now requires an
// explicit ROOT so a Portage CONTENTS path such as /usr/bin/foo can never be
// confused with a host-root path.
func Unmerge(ctx context.Context, pkgPath string) error {
	return fmt.Errorf("unmerge: explicit root and journal are required for %s", pkgPath)
}

// UnmergeConfig identifies the package state removed in one transaction.
type UnmergeConfig struct {
	RootDir        string
	VDBDir         string
	PackagePath    string
	JournalDir     string
	BeforeCommit   func() error
	AllowLiveRoot  bool
	ValidateLocked func() error
	VDBLockHeld    bool // caller owns the operation-wide Portage VDB lock
}

// UnmergeAt transactionally removes one VDB entry and its exclusively-owned
// payload from rootDir. On any failure the payload and VDB entry are restored.
func UnmergeAt(ctx context.Context, rootDir, vdbRoot, pkgPath, journalDir string) error {
	return UnmergeWithConfig(ctx, UnmergeConfig{RootDir: rootDir, VDBDir: vdbRoot, PackagePath: pkgPath, JournalDir: journalDir})
}

// UnmergeWithConfig is the configurable transactional removal entry point.
func UnmergeWithConfig(ctx context.Context, cfg UnmergeConfig) (returnErr error) {
	if cfg.JournalDir == "" {
		return fmt.Errorf("unmerge: journal directory is required")
	}
	rootDir, vdbRoot, pkgPath, journalDir := cfg.RootDir, cfg.VDBDir, cfg.PackagePath, cfg.JournalDir
	if !cfg.VDBLockHeld {
		lock, err := oplock.TryAcquireVDB(vdbRoot)
		if err != nil {
			return fmt.Errorf("unmerge: %w", err)
		}
		defer func() {
			if releaseErr := lock.Release(); releaseErr != nil && returnErr == nil {
				returnErr = fmt.Errorf("unmerge: %w", releaseErr)
			}
		}()
	}
	if cfg.ValidateLocked != nil {
		if err := cfg.ValidateLocked(); err != nil {
			return fmt.Errorf("unmerge: locked state validation: %w", err)
		}
	}
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
	otherOwners, err := ownershipExcluding(vdbRoot, pkgPath)
	if err != nil {
		return fmt.Errorf("unmerge: scan ownership: %w", err)
	}
	if _, err := journal.RecoverActive(journalDir); err != nil {
		return fmt.Errorf("unmerge: recover interrupted journal: %w", err)
	}
	var operation *journal.Journal
	if filepath.Clean(rootDir) == string(filepath.Separator) && cfg.AllowLiveRoot {
		operation, err = journal.BeginLiveRoot(journalDir)
	} else {
		operation, err = journal.Begin(journalDir, rootDir)
	}
	if err != nil {
		return fmt.Errorf("unmerge: begin journal: %w", err)
	}
	rollback := func(cause error) error {
		if rollbackErr := operation.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%v; unmerge: rollback journal %s: %w", cause, operation.Dir(), rollbackErr)
		}
		return fmt.Errorf("%w (rolled back via %s)", cause, operation.Dir())
	}
	sort.SliceStable(entries, func(i, k int) bool {
		return strings.Count(filepath.Clean(entries[i].Path), string(filepath.Separator)) > strings.Count(filepath.Clean(entries[k].Path), string(filepath.Separator))
	})
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return rollback(ctx.Err())
		default:
		}
		canonical, path, pathErr := replacementPath(rootDir, entry.Path)
		if pathErr != nil {
			return rollback(fmt.Errorf("unmerge: %w", pathErr))
		}
		if entry.Type != "dir" && otherOwners[canonical] {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return rollback(fmt.Errorf("unmerge: could not check file %s: %w", path, err))
		}
		if err := operation.Capture(path); err != nil {
			return rollback(fmt.Errorf("unmerge: journal %s: %w", path, err))
		}
		if info.IsDir() {
			if isEmptyDir(path) {
				if err := os.Remove(path); err != nil {
					return rollback(fmt.Errorf("unmerge: could not remove empty directory %s: %w", path, err))
				}
			}
			continue
		}
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return rollback(fmt.Errorf("unmerge: could not remove file %s: %w", path, err))
			}
		}
	}
	if err := operation.RemoveTree(pkgPath); err != nil {
		return rollback(fmt.Errorf("unmerge: remove package database directory: %w", err))
	}
	if cfg.BeforeCommit != nil {
		if err := cfg.BeforeCommit(); err != nil {
			return rollback(fmt.Errorf("unmerge: pre-commit: %w", err))
		}
	}
	if err := operation.Commit(); err != nil {
		return rollback(fmt.Errorf("unmerge: commit journal: %w", err))
	}
	return nil
}

type contentsEntry struct {
	Type  string // "obj", "dir", "sym"
	Path  string
	MD5   string
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

func copyFile(src, dst string, mode os.FileMode, modTime time.Time, stat *syscall.Stat_t) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := in.Close(); cerr != nil { /* Best effort */
		}
	}()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return "", err
	}
	h := md5.New()
	w := io.MultiWriter(out, h)
	if _, err := io.Copy(w, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if stat != nil {
		if err := os.Chown(dst, int(stat.Uid), int(stat.Gid)); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(dst, mode.Perm()|mode&os.ModeSetuid|mode&os.ModeSetgid|mode&os.ModeSticky); err != nil {
		return "", err
	}
	if err := copyXattrs(src, dst, false); err != nil {
		return "", err
	}
	if err := os.Chtimes(dst, modTime, modTime); err != nil {
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
	defer func() {
		if cerr := dh.Close(); cerr != nil { /* Best effort */
		}
	}()
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
