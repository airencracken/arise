package merge

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Journal                  *journal.Journal  // optional active journal begun by the wider rebuild transaction
	AllowLiveRoot            bool              // requires the explicit live-root journal entry point
	AllowLiveReplacement     bool              // exact same-version, lifecycle-free canary only
	VDBLockHeld              bool              // caller owns the operation-wide VDB lock
	ReplacedVDBPath          string            // optional old version entry removed in the same transaction
	VDBMetadata              map[string]string // validated Portage-readable metadata files
	BeforeReplacementRemoval func() error
	AfterReplacementRemoval  func() error
	BeforeCommit             func() error
	AfterPreimageBatch       func() error // recovery-boundary test/instrumentation hook
	AfterPayloadSync         func() error // recovery-boundary test/instrumentation hook
	AfterCommit              func() error // Portage-compatible lifecycle; failure never rolls back the committed package
	ConfigProtect            []string
	ConfigProtectMask        []string
	PreserveLibs             bool
	Environment              []byte // normalized package environment snapshot
	OnStage                  func(stage string)
	OnProgress               func(stage string, current, total int)
}

// PostCommitError reports lifecycle work that failed after payload and VDB
// commit. Callers must not retry or roll back the package transaction as if it
// had failed before commit.
type PostCommitError struct{ Err error }

func (e *PostCommitError) Error() string {
	return fmt.Sprintf("merge: post-commit lifecycle: %v", e.Err)
}
func (e *PostCommitError) Unwrap() error { return e.Err }

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
	if cfg.JournalDir != "" {
		if _, err := journal.RecoverActive(cfg.JournalDir); err != nil {
			return fmt.Errorf("merge: recover interrupted journal: %w", err)
		}
	}
	if cfg.OnStage != nil {
		cfg.OnStage("validate")
	}
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
			if err := validateLiveReplacementTargetsWithConfig(destDir, cfg.RootDir, replacedVDB, cfg.ConfigProtect, cfg.ConfigProtectMask); err != nil {
				return err
			}
		} else if err := validateLiveNewInstallTargets(destDir, cfg.RootDir); err != nil {
			return err
		}
	}
	collisions, err := CheckCollisions(destDir, cfg.VdbDir, []string{cfg.Category + "/" + cfg.Package})
	if err != nil {
		return fmt.Errorf("merge: ownership preflight: %w", err)
	}
	if len(collisions) != 0 {
		return fmt.Errorf("merge: ownership preflight failed: %s", strings.Join(collisions, "; "))
	}
	if cfg.OnStage != nil {
		cfg.OnStage("merge")
	}
	if cfg.JournalDir == "" && cfg.Journal == nil {
		if err := merge(ctx, destDir, cfg, nil); err != nil {
			return err
		}
		if cfg.OnStage != nil {
			cfg.OnStage("finalize")
		}
		if cfg.AfterCommit != nil {
			if err := cfg.AfterCommit(); err != nil {
				return &PostCommitError{Err: err}
			}
		}
		return nil
	}
	j := cfg.Journal
	if j == nil {
		if filepath.Clean(cfg.RootDir) == string(filepath.Separator) && cfg.AllowLiveRoot {
			j, err = journal.BeginLiveRoot(cfg.JournalDir)
		} else {
			j, err = journal.Begin(cfg.JournalDir, cfg.RootDir)
		}
		if err != nil {
			return fmt.Errorf("merge: begin journal: %w", err)
		}
	} else {
		root, rootErr := filepath.Abs(cfg.RootDir)
		if rootErr != nil {
			return fmt.Errorf("merge: validate supplied journal root: %w", rootErr)
		}
		root = filepath.Clean(root)
		wantLive := root == string(filepath.Separator) && cfg.AllowLiveRoot
		if j.Status() != "active" || filepath.Clean(j.Root()) != root || j.LiveRoot() != wantLive {
			return fmt.Errorf("merge: supplied journal does not match active target root")
		}
	}
	if err := merge(ctx, destDir, cfg, j); err != nil {
		if rollbackErr := j.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%v; merge: rollback journal %s: %w", err, j.Dir(), rollbackErr)
		}
		return fmt.Errorf("%w (rolled back via %s)", err, j.Dir())
	}
	if cfg.OnStage != nil {
		cfg.OnStage("commit")
	}
	if err := j.Commit(); err != nil {
		if rollbackErr := j.Rollback(); rollbackErr != nil {
			return fmt.Errorf("merge: commit journal %s: %v; rollback: %w", j.Dir(), err, rollbackErr)
		}
		return fmt.Errorf("merge: commit journal %s: %w", j.Dir(), err)
	}
	if cfg.OnStage != nil {
		cfg.OnStage("finalize")
	}
	if cfg.AfterCommit != nil {
		if err := cfg.AfterCommit(); err != nil {
			return &PostCommitError{Err: err}
		}
	}
	return nil
}

func validateLiveReplacementTargets(destDir, rootDir, vdbPath string) error {
	return validateLiveReplacementTargetsWithConfig(destDir, rootDir, vdbPath, nil, nil)
}

func validateLiveReplacementTargetsWithConfig(destDir, rootDir, vdbPath string, configProtect, configProtectMask []string) error {
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
		lexical := filepath.Clean(entry.Path)
		owned[lexical] = true
		if canonical, err := canonicalLiveOwnershipPath(rootDir, lexical); err == nil {
			owned[canonical] = true
		}
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
		if resolved, err := canonicalLiveOwnershipPath(rootDir, canonical); err == nil {
			canonical = resolved
		}
		if !owned[filepath.Clean(canonical)] {
			if protectedPath(relative, configProtect, configProtectMask) && ownsPendingConfigUpdate(owned, canonical) {
				return nil
			}
			if generatedInfoDirectoryIndex(destDir, relative, entry, info) {
				return nil
			}
			return fmt.Errorf("merge: live replacement target is not owned by replaced package: %s", target)
		}
		return nil
	})
}

func canonicalLiveOwnershipPath(rootDir, recorded string) (string, error) {
	_, target, err := replacementPath(rootDir, recorded)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	return filepath.Clean(contentsPathForRoot(rootDir, resolved)), nil
}

func ownsPendingConfigUpdate(owned map[string]bool, canonical string) bool {
	directory, base := filepath.Dir(filepath.Clean(canonical)), filepath.Base(canonical)
	prefix := filepath.Join(directory, "._cfg")
	suffix := "_" + base
	for path := range owned {
		cleaned := filepath.Clean(path)
		if filepath.Dir(cleaned) != directory {
			continue
		}
		name := filepath.Base(cleaned)
		if !strings.HasPrefix(cleaned, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		counter := strings.TrimSuffix(strings.TrimPrefix(name, "._cfg"), suffix)
		if len(counter) == 4 && counter[0] >= '0' && counter[0] <= '9' && counter[1] >= '0' && counter[1] <= '9' && counter[2] >= '0' && counter[2] <= '9' && counter[3] >= '0' && counter[3] <= '9' {
			return true
		}
	}
	return false
}

func generatedInfoDirectoryIndex(destDir, relative string, staged os.DirEntry, installed os.FileInfo) bool {
	name := filepath.Base(relative)
	if (name != "dir" && name != "dir.gz" && name != "dir.bz2" && name != "dir.xz" && name != "dir.zst") || filepath.Base(filepath.Dir(relative)) != "info" {
		return false
	}
	stagedInfo, err := staged.Info()
	if err != nil || !stagedInfo.Mode().IsRegular() || !installed.Mode().IsRegular() {
		return false
	}
	marker, err := os.Lstat(filepath.Join(destDir, filepath.Dir(relative), ".keepinfodir"))
	return err == nil && marker.Mode().IsRegular()
}

// validateLiveNewInstallTargets limits the first live lane to additive package
// state. Existing directories may be shared. An identical symlink may be
// adopted because alternatives packages can stage a link already created by a
// provider's post-install lifecycle; no filesystem object is changed in that
// case. Every other existing file, differing symlink or special object is
// refused, whether VDB-owned or local/unowned.
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
		if entry.Type()&os.ModeSymlink != 0 && info.Mode()&os.ModeSymlink != 0 {
			stagedLink, stagedErr := os.Readlink(source)
			installedLink, installedErr := os.Readlink(target)
			if stagedErr != nil {
				return fmt.Errorf("merge: read staged live canary symlink %s: %w", source, stagedErr)
			}
			if installedErr != nil {
				return fmt.Errorf("merge: read installed live canary symlink %s: %w", target, installedErr)
			}
			if stagedLink == installedLink {
				return nil
			}
		}
		return fmt.Errorf("merge: live new-install canary refuses existing target %s", target)
	})
}

func merge(ctx context.Context, destDir string, cfg MergeConfig, operation *journal.Journal) error {
	mergeTime := time.Now().Unix()
	vdbDir := cfg.vdbPath()
	if operation != nil {
		vdbExisted := false
		if _, err := os.Lstat(vdbDir); err == nil {
			vdbExisted = true
			if err := operation.RemoveTree(vdbDir); err != nil {
				return fmt.Errorf("merge: journal existing package database directory: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("merge: inspect package database directory %s: %w", vdbDir, err)
		}
		if vdbExisted {
			if err := operation.Capture(vdbDir); err != nil {
				return fmt.Errorf("merge: journal package database directory: %w", err)
			}
		} else if err := operation.CaptureAbsentTree(vdbDir); err != nil {
			return fmt.Errorf("merge: journal new package database subtree: %w", err)
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
	var absentSubtreeRoots []string
	totalPaths := 0
	insideAbsentSubtree := func(path string) bool {
		path = filepath.Clean(path)
		for _, root := range absentSubtreeRoots {
			relative, err := filepath.Rel(root, path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	if operation != nil {
		var capturePaths []string
		if err := filepath.WalkDir(destDir, func(srcPath string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			rel, err := filepath.Rel(destDir, srcPath)
			if err != nil || rel == "." {
				return err
			}
			totalPaths++
			targetPath := filepath.Join(cfg.RootDir, rel)
			if insideAbsentSubtree(targetPath) {
				return nil
			}
			info, statErr := os.Lstat(targetPath)
			if d.IsDir() && os.IsNotExist(statErr) {
				if err := operation.CaptureAbsentTree(targetPath); err != nil {
					return fmt.Errorf("merge: journal new subtree %s: %w", targetPath, err)
				}
				absentSubtreeRoots = append(absentSubtreeRoots, filepath.Clean(targetPath))
				return nil
			}
			if statErr != nil {
				if os.IsNotExist(statErr) {
					capturePaths = append(capturePaths, targetPath)
					return nil
				}
				// A non-directory target may be replaced by an earlier directory
				// entry in the mutation pass. Its parent preimage is already in
				// the batch; descendants retain conservative inline capture.
				if errors.Is(statErr, syscall.ENOTDIR) {
					return nil
				}
				return fmt.Errorf("merge: inspect preimage %s: %w", targetPath, statErr)
			}
			if d.Type().IsRegular() && protectedPath(rel, cfg.ConfigProtect, cfg.ConfigProtectMask) && info != nil && info.Mode().IsRegular() {
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
			}
			capturePaths = append(capturePaths, targetPath)
			return nil
		}); err != nil {
			return err
		}
		if err := operation.CaptureBatch(capturePaths); err != nil {
			return fmt.Errorf("merge: publish preimage batch: %w", err)
		}
		if cfg.AfterPreimageBatch != nil {
			if err := cfg.AfterPreimageBatch(); err != nil {
				return fmt.Errorf("merge: after preimage batch: %w", err)
			}
		}
	}
	processedPaths := 0
	lastProgress := time.Time{}

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
		defer func() {
			processedPaths++
			if cfg.OnProgress != nil && (processedPaths == totalPaths || lastProgress.IsZero() || time.Since(lastProgress) >= time.Second) {
				cfg.OnProgress("merge", processedPaths, totalPaths)
				lastProgress = time.Now()
			}
		}()

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
			} else if operation != nil && !insideAbsentSubtree(targetPath) {
				if created {
					if err := operation.CaptureAbsentTree(targetPath); err != nil {
						return fmt.Errorf("merge: journal new subtree %s: %w", targetPath, err)
					}
					absentSubtreeRoots = append(absentSubtreeRoots, filepath.Clean(targetPath))
				} else if err := operation.Capture(targetPath); err != nil {
					return fmt.Errorf("merge: journal target %s: %w", targetPath, err)
				}
			}
			if err := os.MkdirAll(targetPath, info.Mode()); err != nil {
				return fmt.Errorf("merge: could not create directory %s: %w", targetPath, err)
			}
			// Portage applies the image directory's metadata even when the live
			// directory already exists. Service packages commonly rely on this
			// to repair account-created state directories during installation.
			createdDirectories = append(createdDirectories, createdDirectory{path: targetPath, info: info})
			lines = append(lines, formatContentsDir(contentsPathForRoot(cfg.RootDir, targetPath)))
			return nil

		case d.Type()&os.ModeSymlink != 0:
			targetOperation := operation
			if insideAbsentSubtree(targetPath) {
				targetOperation = nil
			}
			if err := prepareNonDirectoryTarget(targetOperation, targetPath); err != nil {
				return fmt.Errorf("merge: prepare symlink target %s: %w", targetPath, err)
			}
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("merge: could not read symlink %s: %w", srcPath, err)
			}
			if cfg.VDBMetadata["EAPI"] != "9" && filepath.IsAbs(linkTarget) {
				cleanImage := filepath.Clean(destDir)
				cleanTarget := filepath.Clean(linkTarget)
				if relative, relErr := filepath.Rel(cleanImage, cleanTarget); relErr == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					linkTarget = filepath.Join(string(filepath.Separator), relative)
				}
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
				if targetInfo, statErr := os.Lstat(targetPath); statErr == nil && targetInfo.Mode().IsRegular() {
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
				} else if statErr != nil && !os.IsNotExist(statErr) {
					return statErr
				}
			}
			targetOperation := operation
			if insideAbsentSubtree(targetPath) {
				targetOperation = nil
			}
			if err := prepareNonDirectoryTarget(targetOperation, targetPath); err != nil {
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
	contents := []byte(nil)
	if len(lines) != 0 {
		contents = []byte(strings.Join(lines, "\n") + "\n")
	}
	if err := os.WriteFile(contentsPath, contents, 0644); err != nil {
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
	if operation != nil && cfg.PreserveLibs {
		if err := prunePreservedRegistry(operation, cfg); err != nil {
			return err
		}
	}
	if operation != nil && cfg.BeforeCommit != nil {
		if err := cfg.BeforeCommit(); err != nil {
			return fmt.Errorf("merge: pre-commit lifecycle: %w", err)
		}
	}
	if operation != nil {
		if cfg.OnStage != nil {
			cfg.OnStage("sync")
		}
		if err := syncFilesystems(cfg.RootDir, cfg.VdbDir); err != nil {
			return fmt.Errorf("merge: sync transaction payload: %w", err)
		}
		if cfg.AfterPayloadSync != nil {
			if err := cfg.AfterPayloadSync(); err != nil {
				return fmt.Errorf("merge: after payload sync: %w", err)
			}
		}
	}

	return nil
}

func syncFilesystems(paths ...string) error {
	seen := make(map[uint64]bool)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("stat %s has no device identity", path)
		}
		device := uint64(stat.Dev)
		if seen[device] {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		syncErr := unix.Syncfs(int(file.Fd()))
		closeErr := file.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		seen[device] = true
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
		canonical := filepath.Clean(path)
		if resolved, resolveErr := canonicalLiveOwnershipPath(cfg.RootDir, canonical); resolveErr == nil {
			canonical = resolved
		}
		retained[canonical] = true
	}
	otherOwners, err := ownershipExcluding(cfg.VdbDir, oldVDB, newVDB)
	if err != nil {
		return fmt.Errorf("merge: scan replacement ownership: %w", err)
	}
	preservedPaths := make(map[string]bool)
	if cfg.PreserveLibs {
		preservedPaths, err = requiredPreservedPaths(cfg.RootDir, cfg.VdbDir, oldVDB, newVDB, entries)
		if err != nil {
			return fmt.Errorf("merge: select preserved libraries: %w", err)
		}
	}
	sort.SliceStable(entries, func(i, k int) bool {
		return strings.Count(filepath.Clean(entries[i].Path), string(filepath.Separator)) > strings.Count(filepath.Clean(entries[k].Path), string(filepath.Separator))
	})
	for _, entry := range entries {
		canonical, target, err := replacementPath(cfg.RootDir, entry.Path)
		if err != nil {
			return err
		}
		if resolved, resolveErr := canonicalLiveOwnershipPath(cfg.RootDir, canonical); resolveErr == nil {
			canonical = resolved
		}
		if retained[canonical] || otherOwners[canonical] || preservedPaths[canonical] {
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
	if len(preservedPaths) != 0 {
		if err := updatePreservedRegistry(operation, cfg, newVDB, preservedPaths); err != nil {
			return err
		}
	}
	return nil
}

func requiredPreservedPaths(root, vdbRoot, oldVDB, newVDB string, entries []contentsEntry) (map[string]bool, error) {
	oldProvided, err := neededProviders(filepath.Join(oldVDB, "NEEDED.ELF.2"))
	if err != nil {
		return nil, err
	}
	// Installed linkage metadata can be missing or stale (notably for packages
	// merged by older package managers). Inspect the actual obsolete ELF
	// objects before deleting them so preserve-libs never depends solely on
	// provider metadata being complete.
	for _, entry := range entries {
		if entry.Type != "obj" {
			continue
		}
		canonical := filepath.Clean(entry.Path)
		fullPath := filepath.Join(root, strings.TrimPrefix(canonical, string(filepath.Separator)))
		if soname := elfSONAME(fullPath); soname != "" {
			oldProvided[soname] = canonical
		}
	}
	newProvided, err := neededProviders(filepath.Join(newVDB, "NEEDED.ELF.2"))
	if err != nil {
		return nil, err
	}
	needed := make(map[string]bool)
	categories, err := os.ReadDir(vdbRoot)
	if err != nil {
		return nil, err
	}
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		packages, _ := os.ReadDir(filepath.Join(vdbRoot, category.Name()))
		for _, pkg := range packages {
			path := filepath.Join(vdbRoot, category.Name(), pkg.Name())
			if !pkg.IsDir() || filepath.Clean(path) == filepath.Clean(oldVDB) || filepath.Clean(path) == filepath.Clean(newVDB) {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(path, "NEEDED.ELF.2"))
			if readErr != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Split(line, ";")
				if len(fields) >= 5 {
					for _, soname := range strings.Split(fields[4], ",") {
						needed[strings.TrimSpace(soname)] = true
					}
				}
			}
		}
	}
	preserved := make(map[string]bool)
	for soname, providerPath := range oldProvided {
		if !needed[soname] || newProvided[soname] != "" {
			continue
		}
		providerPath = filepath.Clean(providerPath)
		preserved[providerPath] = true
		providerDir := filepath.Dir(providerPath)
		for _, entry := range entries {
			candidate := filepath.Clean(entry.Path)
			if entry.Type == "sym" && filepath.Dir(candidate) == providerDir {
				base := filepath.Base(candidate)
				// Preserve the runtime SONAME link, never the unversioned
				// development link. The latter must continue to select the new
				// provider ABI after the upgrade.
				if base == soname {
					preserved[candidate] = true
				}
			}
		}
	}
	return preserved, nil
}

func neededProviders(path string) (map[string]string, error) {
	providers := make(map[string]string)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return providers, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ";")
		if len(fields) >= 3 && fields[1] != "" && fields[2] != "" {
			providers[fields[2]] = filepath.Clean(fields[1])
		}
	}
	return providers, nil
}

func updatePreservedRegistry(operation *journal.Journal, cfg MergeConfig, ownerVDB string, paths map[string]bool) error {
	registryPath := filepath.Join(cfg.RootDir, "var", "lib", "portage", "preserved_libs_registry")
	records := make(map[string][]json.RawMessage)
	if data, err := os.ReadFile(registryPath); err == nil && len(strings.TrimSpace(string(data))) != 0 {
		if err := json.Unmarshal(data, &records); err != nil {
			return fmt.Errorf("merge: parse preserved library registry: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	ownerCPV, err := filepath.Rel(cfg.VdbDir, ownerVDB)
	if err != nil || strings.HasPrefix(ownerCPV, "..") {
		return fmt.Errorf("merge: invalid preserved library owner %s", ownerVDB)
	}
	slotData, err := os.ReadFile(filepath.Join(ownerVDB, "SLOT"))
	if err != nil {
		return err
	}
	counterData, err := os.ReadFile(filepath.Join(ownerVDB, "COUNTER"))
	if err != nil {
		return err
	}
	registered := make([]string, 0, len(paths))
	for path := range paths {
		registered = append(registered, filepath.ToSlash(path))
	}
	sort.Strings(registered)
	ownerJSON, _ := json.Marshal(filepath.ToSlash(ownerCPV))
	counterJSON, _ := json.Marshal(strings.TrimSpace(string(counterData)))
	pathsJSON, _ := json.Marshal(registered)
	key := cfg.Category + "/" + cfg.Package + ":" + strings.SplitN(strings.TrimSpace(string(slotData)), "/", 2)[0]
	records[key] = []json.RawMessage{ownerJSON, counterJSON, pathsJSON}
	// Portage transfers ownership of preserved objects to the new provider's
	// VDB entry. Without these CONTENTS records, later collision checks and the
	// final preserved-library prune cannot safely distinguish them from
	// unowned filesystem debris.
	contentsPath := filepath.Join(ownerVDB, "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		return err
	}
	owned := make(map[string]bool)
	for _, entry := range strings.Split(string(contentsData), "\n") {
		fields := strings.Fields(entry)
		if len(fields) >= 2 {
			owned[filepath.Clean(fields[1])] = true
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(contentsData), "\n"), "\n")
	for _, registeredPath := range registered {
		canonical := filepath.Clean(registeredPath)
		if owned[canonical] {
			continue
		}
		fullPath := filepath.Join(cfg.RootDir, strings.TrimPrefix(canonical, string(filepath.Separator)))
		info, statErr := os.Lstat(fullPath)
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(fullPath)
			if readErr != nil {
				return readErr
			}
			sum, _ := md5Bytes([]byte(target))
			lines = append(lines, formatContentsSym(canonical, target, sum, info.ModTime().Unix()))
		} else if info.Mode().IsRegular() {
			sum, hashErr := md5File(fullPath)
			if hashErr != nil {
				return hashErr
			}
			lines = append(lines, formatContentsObj(canonical, sum, info.ModTime().Unix()))
		}
	}
	if err := operation.Capture(contentsPath); err != nil {
		return err
	}
	if err := os.WriteFile(contentsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "\t")
	if err != nil {
		return err
	}
	if err := ensureJournaledParent(operation, cfg.RootDir, registryPath); err != nil {
		return err
	}
	if operation != nil {
		if err := operation.Capture(registryPath); err != nil {
			return err
		}
	}
	if err := os.WriteFile(registryPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}

func prunePreservedRegistry(operation *journal.Journal, cfg MergeConfig) error {
	registryPath := filepath.Join(cfg.RootDir, "var", "lib", "portage", "preserved_libs_registry")
	data, err := os.ReadFile(registryPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	records := make(map[string][]json.RawMessage)
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("merge: parse preserved library registry: %w", err)
	}
	needed := make(map[string]bool)
	_ = filepath.WalkDir(cfg.VdbDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "NEEDED.ELF.2" {
			return nil
		}
		metadata, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, line := range strings.Split(string(metadata), "\n") {
			fields := strings.Split(line, ";")
			if len(fields) >= 5 {
				for _, soname := range strings.Split(fields[4], ",") {
					needed[strings.TrimSpace(soname)] = true
				}
			}
		}
		return nil
	})
	changed := false
	for key, record := range records {
		if len(record) != 3 {
			continue
		}
		var paths []string
		var recordedOwner string
		if err := json.Unmarshal(record[0], &recordedOwner); err != nil || json.Unmarshal(record[2], &paths) != nil {
			continue
		}
		registeredRegular := make(map[string]bool)
		for _, registered := range paths {
			canonical := filepath.Clean(registered)
			fullPath := filepath.Join(cfg.RootDir, strings.TrimPrefix(canonical, string(filepath.Separator)))
			if info, statErr := os.Lstat(fullPath); statErr == nil && info.Mode().IsRegular() {
				registeredRegular[canonical] = true
			}
		}
		required := false
		// Registry records can contain symlinks that were part of the old ABI
		// chain.  A later provider merge may retarget an unversioned link to the
		// new ABI without rewriting the old registry record.  Following that
		// link here would make a needed current SONAME keep an unrelated old
		// preserved object forever. Only regular preserved ELF objects and links
		// that still point to those objects decide whether the record is needed.
		for _, registered := range paths {
			canonical := filepath.Clean(registered)
			fullPath := filepath.Join(cfg.RootDir, strings.TrimPrefix(canonical, string(filepath.Separator)))
			info, statErr := os.Lstat(fullPath)
			if statErr != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, readErr := os.Readlink(fullPath)
				if readErr != nil {
					continue
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(canonical), target)
				}
				if !registeredRegular[filepath.Clean(target)] {
					continue
				}
			} else if !info.Mode().IsRegular() {
				continue
			}
			soname := filepath.Base(registered)
			if inspected := elfSONAME(fullPath); inspected != "" {
				soname = inspected
			}
			if needed[soname] {
				required = true
				break
			}
		}
		if required {
			continue
		}
		ownerVDB := filepath.Join(cfg.VdbDir, filepath.FromSlash(recordedOwner))
		otherOwners, err := ownershipExcluding(cfg.VdbDir, ownerVDB)
		if err != nil {
			return err
		}
		removableRegular := make(map[string]bool)
		for _, registered := range paths {
			canonical := filepath.Clean(registered)
			if otherOwners[canonical] {
				continue
			}
			fullPath := filepath.Join(cfg.RootDir, strings.TrimPrefix(canonical, string(filepath.Separator)))
			if info, statErr := os.Lstat(fullPath); statErr == nil && info.Mode().IsRegular() {
				removableRegular[canonical] = true
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return statErr
			}
		}
		removed := make(map[string]bool)
		for _, registered := range paths {
			canonical := filepath.Clean(registered)
			if otherOwners[canonical] {
				continue
			}
			fullPath := filepath.Join(cfg.RootDir, strings.TrimPrefix(canonical, string(filepath.Separator)))
			if _, err := os.Lstat(fullPath); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			info, err := os.Lstat(fullPath)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(fullPath)
				if err != nil {
					return err
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(canonical), target)
				}
				if !removableRegular[filepath.Clean(target)] {
					// This link now selects the current ABI and is no longer a
					// preserved object, despite its stale registry membership.
					continue
				}
			} else if !info.Mode().IsRegular() {
				continue
			}
			if err := operation.Capture(fullPath); err != nil {
				return err
			}
			if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			removed[canonical] = true
		}
		if len(removed) != 0 {
			contentsPath := filepath.Join(ownerVDB, "CONTENTS")
			contents, readErr := os.ReadFile(contentsPath)
			if readErr == nil {
				var retainedLines []string
				for _, line := range strings.Split(string(contents), "\n") {
					fields := strings.Fields(line)
					if len(fields) >= 2 && removed[filepath.Clean(fields[1])] {
						continue
					}
					if line != "" {
						retainedLines = append(retainedLines, line)
					}
				}
				if err := operation.Capture(contentsPath); err != nil {
					return err
				}
				if err := os.WriteFile(contentsPath, []byte(strings.Join(retainedLines, "\n")+"\n"), 0o644); err != nil {
					return err
				}
			} else if !os.IsNotExist(readErr) {
				return readErr
			}
		}
		delete(records, key)
		changed = true
	}
	if !changed {
		return nil
	}
	encoded, err := json.MarshalIndent(records, "", "\t")
	if err != nil {
		return err
	}
	if err := operation.Capture(registryPath); err != nil {
		return err
	}
	return os.WriteFile(registryPath, append(encoded, '\n'), 0o644)
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
	BeforeRemoval  func() error
	AfterRemoval   func() error
	BeforeCommit   func() error
	AfterCommit    func() error
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
	if _, err := journal.RecoverActive(journalDir); err != nil {
		return fmt.Errorf("unmerge: recover interrupted journal: %w", err)
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
	if cfg.BeforeRemoval != nil {
		if err := cfg.BeforeRemoval(); err != nil {
			return rollback(fmt.Errorf("unmerge: pre-removal lifecycle: %w", err))
		}
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
	if cfg.AfterRemoval != nil {
		if err := cfg.AfterRemoval(); err != nil {
			return rollback(fmt.Errorf("unmerge: post-removal lifecycle: %w", err))
		}
	}
	if err := prunePreservedRegistry(operation, MergeConfig{RootDir: rootDir, VdbDir: vdbRoot}); err != nil {
		return rollback(fmt.Errorf("unmerge: prune preserved libraries: %w", err))
	}
	if cfg.BeforeCommit != nil {
		if err := cfg.BeforeCommit(); err != nil {
			return rollback(fmt.Errorf("unmerge: pre-commit: %w", err))
		}
	}
	if err := operation.Commit(); err != nil {
		return rollback(fmt.Errorf("unmerge: commit journal: %w", err))
	}
	if cfg.AfterCommit != nil {
		if err := cfg.AfterCommit(); err != nil {
			return &PostCommitError{Err: err}
		}
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
		typeEnd := strings.IndexByte(line, ' ')
		if typeEnd < 1 {
			continue
		}
		e := contentsEntry{Type: line[:typeEnd]}
		body := strings.TrimSpace(line[typeEnd+1:])
		switch e.Type {
		case "obj":
			mtimeAt := strings.LastIndexByte(body, ' ')
			if mtimeAt < 1 {
				continue
			}
			e.Mtime, _ = strconv.ParseInt(strings.TrimSpace(body[mtimeAt+1:]), 10, 64)
			pathAndMD5 := strings.TrimSpace(body[:mtimeAt])
			md5At := strings.LastIndexByte(pathAndMD5, ' ')
			if md5At < 1 {
				continue
			}
			e.Path = strings.TrimSpace(pathAndMD5[:md5At])
			e.MD5 = strings.TrimSpace(pathAndMD5[md5At+1:])
		case "sym":
			arrow := strings.Index(body, " -> ")
			if arrow < 1 {
				continue
			}
			e.Path = strings.TrimSpace(body[:arrow])
			tail := strings.TrimSpace(body[arrow+4:])
			mtimeAt := strings.LastIndexByte(tail, ' ')
			if mtimeAt >= 0 {
				e.Mtime, _ = strconv.ParseInt(strings.TrimSpace(tail[mtimeAt+1:]), 10, 64)
			}
		case "dir", "fif", "dev":
			e.Path = body
		default:
			continue
		}
		if e.Path == "" {
			continue
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

func md5File(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	h := md5.New()
	if _, err := io.Copy(h, input); err != nil {
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
