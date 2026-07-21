// Package journal provides a durable, path-confined filesystem undo journal.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const Version = 3

const entriesLogName = "entries.jsonl"

type Xattr struct {
	Name  string `json:"name"`
	Value []byte `json:"value"`
}

type Entry struct {
	Path   string      `json:"path"`
	Kind   string      `json:"kind"` // absent, file, directory, symlink
	Mode   os.FileMode `json:"mode,omitempty"`
	Link   string      `json:"link,omitempty"`
	Backup string      `json:"backup,omitempty"`
	UID    uint32      `json:"uid,omitempty"`
	GID    uint32      `json:"gid,omitempty"`
	ATime  int64       `json:"atime_ns,omitempty"`
	MTime  int64       `json:"mtime_ns,omitempty"`
	Xattrs []Xattr     `json:"xattrs,omitempty"`
}

type State struct {
	Version  int     `json:"version"`
	Status   string  `json:"status"`
	Root     string  `json:"root"`
	LiveRoot bool    `json:"live_root,omitempty"`
	Entries  []Entry `json:"entries"`
}

type Journal struct {
	mu       sync.Mutex
	dir      string
	state    State
	captured map[string]bool
}

type Summary struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Root    string `json:"root"`
	Entries int    `json:"entries"`
}

// List returns deterministic journal metadata without modifying recovery
// state. Invalid journal directories fail visibly instead of being skipped.
func List(base string) ([]Summary, error) {
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: list operations: %w", err)
	}
	var summaries []Summary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(base, entry.Name())
		operation, err := Open(path)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, Summary{ID: entry.Name(), Path: path, Status: operation.Status(), Root: operation.state.Root, Entries: len(operation.state.Entries)})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ID < summaries[j].ID })
	if summaries == nil {
		summaries = []Summary{}
	}
	return summaries, nil
}

// RollbackActive opens and rolls back one named active journal. The identifier
// must be a direct child of base; paths and traversal are rejected.
func RollbackActive(base, id string) (Summary, error) {
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return Summary{}, fmt.Errorf("journal: invalid operation identifier %q", id)
	}
	path := filepath.Join(base, id)
	operation, err := Open(path)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{ID: id, Path: path, Status: operation.Status(), Root: operation.state.Root, Entries: len(operation.state.Entries)}
	if operation.Status() != "active" {
		return summary, fmt.Errorf("journal: operation %s is %s, not active", id, operation.Status())
	}
	if err := operation.Rollback(); err != nil {
		return summary, err
	}
	summary.Status = operation.Status()
	return summary, nil
}

func Begin(base, root string) (*Journal, error) {
	return begin(base, root, false)
}

// BeginLiveRoot is the deliberately explicit entry point for a transaction
// rooted at /. Callers must independently enforce plan authorization and
// lifecycle eligibility before using it.
func BeginLiveRoot(base string) (*Journal, error) {
	return begin(base, string(filepath.Separator), true)
}

func begin(base, root string, allowLiveRoot bool) (*Journal, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	if root == string(filepath.Separator) && !allowLiveRoot {
		return nil, fmt.Errorf("journal: refusing filesystem root transaction")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, fmt.Errorf("journal: create base: %w", err)
	}
	dir, err := os.MkdirTemp(base, "operation-")
	if err != nil {
		return nil, fmt.Errorf("journal: create operation: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "backups"), 0o700); err != nil {
		return nil, fmt.Errorf("journal: create backups directory: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(dir, entriesLogName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: create capture log: %w", err)
	}
	if err = logFile.Sync(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("journal: sync capture log: %w", err)
	}
	if err := logFile.Close(); err != nil {
		return nil, fmt.Errorf("journal: close capture log: %w", err)
	}
	j := &Journal{dir: dir, state: State{Version: Version, Status: "active", Root: root, LiveRoot: allowLiveRoot}, captured: make(map[string]bool)}
	if err := j.persist(); err != nil {
		return nil, err
	}
	return j, nil
}

func Open(dir string) (*Journal, error) {
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return nil, fmt.Errorf("journal: read state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("journal: decode state: %w", err)
	}
	if (state.Version < 1 || state.Version > Version) || state.Root == "" || !filepath.IsAbs(state.Root) || (filepath.Clean(state.Root) == string(filepath.Separator) && !state.LiveRoot) || (filepath.Clean(state.Root) != string(filepath.Separator) && state.LiveRoot) {
		return nil, fmt.Errorf("journal: invalid state")
	}
	if state.Version >= 3 && state.Status == "active" {
		logged, err := readEntryLog(dir)
		if err != nil {
			return nil, err
		}
		state.Entries = append(state.Entries, logged...)
	}
	j := &Journal{dir: dir, state: state, captured: make(map[string]bool)}
	for _, entry := range state.Entries {
		j.captured[entry.Path] = true
	}
	return j, nil
}

func (j *Journal) Dir() string    { return j.dir }
func (j *Journal) Status() string { return j.state.Status }

// Capture durably records a path's preimage before its first mutation.
func (j *Journal) Capture(path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.Status != "active" {
		return fmt.Errorf("journal: capture on %s operation", j.state.Status)
	}
	resolved, relative, err := j.confined(path)
	if err != nil {
		return err
	}
	if j.captured[relative] {
		return nil
	}
	entry := Entry{Path: relative, Kind: "absent"}
	info, err := os.Lstat(resolved)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("journal: inspect %s: %w", resolved, err)
	}
	if err == nil {
		entry.Mode = info.Mode()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			entry.UID, entry.GID = stat.Uid, stat.Gid
			entry.ATime, entry.MTime = stat.Atim.Sec*1e9+stat.Atim.Nsec, stat.Mtim.Sec*1e9+stat.Mtim.Nsec
		}
		entry.Xattrs, err = readXattrs(resolved)
		if err != nil {
			return fmt.Errorf("journal: capture xattrs %s: %w", resolved, err)
		}
		switch {
		case info.Mode().IsRegular():
			entry.Kind = "file"
			entry.Backup = fmt.Sprintf("backups/%08d", len(j.state.Entries))
			if err := copyFile(resolved, filepath.Join(j.dir, entry.Backup), info.Mode()); err != nil {
				return fmt.Errorf("journal: back up %s: %w", resolved, err)
			}
			if err := syncDirectory(filepath.Join(j.dir, "backups")); err != nil {
				return fmt.Errorf("journal: sync backup for %s: %w", resolved, err)
			}
		case info.IsDir():
			entry.Kind = "directory"
		case info.Mode()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			entry.Link, err = os.Readlink(resolved)
			if err != nil {
				return fmt.Errorf("journal: read link %s: %w", resolved, err)
			}
		default:
			return fmt.Errorf("journal: unsupported preimage type at %s", resolved)
		}
	}
	j.state.Entries = append(j.state.Entries, entry)
	if err := j.appendEntry(entry); err != nil {
		j.state.Entries = j.state.Entries[:len(j.state.Entries)-1]
		return err
	}
	j.captured[relative] = true
	return nil
}

// appendEntry publishes one durable capture record without rewriting the
// complete transaction state. The caller has already synced any regular-file
// backup, and no filesystem mutation may occur until this record is synced.
func (j *Journal) appendEntry(entry Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	path := filepath.Join(j.dir, entriesLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("journal: open capture log: %w", err)
	}
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("journal: append capture log: %w", err)
	}
	return nil
}

func readEntryLog(dir string) ([]Entry, error) {
	path := filepath.Join(dir, entriesLogName)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: open capture log: %w", err)
	}
	defer file.Close()
	var entries []Entry
	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			// Capture does not return (and mutation therefore cannot begin) until
			// the newline-terminated record is synced. A torn final record is safe
			// to ignore during crash recovery.
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("journal: read capture log: %w", readErr)
		}
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("journal: decode capture log entry %d: %w", len(entries)+1, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// CaptureTree records every existing entry below path without following
// symlinks. WalkDir's lexical order makes the journal deterministic.
func (j *Journal) CaptureTree(path string) error {
	resolved, _, err := j.confined(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(resolved); os.IsNotExist(err) {
		return j.Capture(resolved)
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(resolved, func(current string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return j.Capture(current)
	})
}

// RemoveTree removes a captured tree from leaves to root. Every removal is
// recoverable because CaptureTree is durably complete before mutation begins.
func (j *Journal) RemoveTree(path string) error {
	resolved, _, err := j.confined(path)
	if err != nil {
		return err
	}
	if err := j.CaptureTree(resolved); err != nil {
		return err
	}
	var paths []string
	if err := filepath.WalkDir(resolved, func(current string, _ os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		paths = append(paths, current)
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Remove(paths[index]); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("journal: remove %s: %w", paths[index], err)
		}
	}
	return nil
}

func (j *Journal) Commit() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.Status != "active" {
		return fmt.Errorf("journal: commit on %s operation", j.state.Status)
	}
	j.state.Status = "committed"
	if err := j.persist(); err != nil {
		j.state.Status = "active"
		return err
	}
	return nil
}

// RecoverActive rolls back durable operations that did not reach commit.
func RecoverActive(base string) ([]string, error) {
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: list operations: %w", err)
	}
	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, filepath.Join(base, entry.Name()))
		}
	}
	sort.Strings(directories)
	var recovered []string
	for _, directory := range directories {
		operation, err := Open(directory)
		if err != nil {
			return recovered, err
		}
		if operation.Status() != "active" {
			continue
		}
		if err := operation.Rollback(); err != nil {
			return recovered, fmt.Errorf("journal: recover %s: %w", directory, err)
		}
		recovered = append(recovered, directory)
	}
	return recovered, nil
}

func (j *Journal) Rollback() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.Status == "rolled-back" {
		return nil
	}
	if j.state.Status == "committed" {
		return fmt.Errorf("journal: committed operation cannot roll back")
	}
	for index := len(j.state.Entries) - 1; index >= 0; index-- {
		entry := j.state.Entries[index]
		target, _, err := j.confined(filepath.Join(j.state.Root, entry.Path))
		if err != nil {
			return err
		}
		if err := restore(j.dir, target, entry); err != nil {
			return fmt.Errorf("journal: restore %s: %w", target, err)
		}
	}
	j.state.Status = "rolled-back"
	return j.persist()
}

func (j *Journal) confined(path string) (string, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	abs = filepath.Clean(abs)
	relative, err := filepath.Rel(j.state.Root, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("journal: path %s escapes transaction root %s", abs, j.state.Root)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) <= 1 {
		return abs, relative, nil
	}
	// Resolve ancestor symlinks component-by-component while deliberately not
	// following the final component: Capture must be able to journal a symlink
	// itself. Canonicalizing ancestors makes merged-usr paths such as
	// /lib/udev -> /usr/lib/udev journal and roll back the actual target.
	queue := append([]string(nil), parts[:len(parts)-1]...)
	final := parts[len(parts)-1]
	current := j.state.Root
	links := 0
	for len(queue) > 0 {
		part := queue[0]
		queue = queue[1:]
		if part == "" || part == "." {
			continue
		}
		candidate := filepath.Join(current, part)
		info, statErr := os.Lstat(candidate)
		if os.IsNotExist(statErr) {
			current = candidate
			for _, remainder := range queue {
				current = filepath.Join(current, remainder)
			}
			break
		}
		if statErr != nil {
			return "", "", fmt.Errorf("journal: inspect ancestor %s: %w", candidate, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 255 {
				return "", "", fmt.Errorf("journal: too many symlink ancestors resolving %s", abs)
			}
			target, err := os.Readlink(candidate)
			if err != nil {
				return "", "", fmt.Errorf("journal: read ancestor link %s: %w", candidate, err)
			}
			if filepath.IsAbs(target) {
				if j.state.Root != string(filepath.Separator) {
					return "", "", fmt.Errorf("journal: absolute symlink ancestor %s is unsafe outside a live-root transaction", candidate)
				}
				current = j.state.Root
				target = strings.TrimPrefix(filepath.Clean(target), string(filepath.Separator))
			} else {
				current = filepath.Dir(candidate)
			}
			targetAbs := filepath.Clean(filepath.Join(current, target))
			targetRelative, err := filepath.Rel(j.state.Root, targetAbs)
			if err != nil || targetRelative == ".." || strings.HasPrefix(targetRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(targetRelative) {
				return "", "", fmt.Errorf("journal: symlink ancestor %s escapes transaction root %s", candidate, j.state.Root)
			}
			current = j.state.Root
			queue = append(strings.Split(targetRelative, string(filepath.Separator)), queue...)
			continue
		}
		current = candidate
	}
	resolved := filepath.Join(current, final)
	resolvedRelative, err := filepath.Rel(j.state.Root, resolved)
	if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(resolvedRelative) {
		return "", "", fmt.Errorf("journal: resolved path %s escapes transaction root %s", resolved, j.state.Root)
	}
	return resolved, resolvedRelative, nil
}

func (j *Journal) persist() error {
	data, err := json.MarshalIndent(j.state, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(j.dir, "state.json.tmp")
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("journal: create state: %w", err)
	}
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("journal: write state: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(j.dir, "state.json")); err != nil {
		return fmt.Errorf("journal: publish state: %w", err)
	}
	return syncDirectory(j.dir)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func restore(journalDir, target string, entry Entry) error {
	info, err := os.Lstat(target)
	if err == nil {
		if info.IsDir() {
			if err := os.Remove(target); err != nil && entry.Kind != "directory" {
				return err
			}
		} else if err := os.Remove(target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	switch entry.Kind {
	case "absent":
		return nil
	case "directory":
		if err := os.MkdirAll(target, entry.Mode.Perm()); err != nil {
			return err
		}
	case "symlink":
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(entry.Link, target); err != nil {
			return err
		}
	case "file":
		if err := copyFile(filepath.Join(journalDir, entry.Backup), target, entry.Mode); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown entry kind %q", entry.Kind)
	}
	if entry.Kind == "symlink" {
		if err := os.Lchown(target, int(entry.UID), int(entry.GID)); err != nil {
			return err
		}
	} else {
		if err := os.Chown(target, int(entry.UID), int(entry.GID)); err != nil {
			return err
		}
	}
	if err := writeXattrs(target, entry.Xattrs); err != nil {
		return err
	}
	// Chown and security xattr updates may clear set-ID bits, so mode is the
	// final metadata operation for regular files and directories.
	if entry.Kind != "symlink" {
		if err := os.Chmod(target, entry.Mode.Perm()|entry.Mode&os.ModeSetuid|entry.Mode&os.ModeSetgid|entry.Mode&os.ModeSticky); err != nil {
			return err
		}
	}
	if entry.ATime != 0 || entry.MTime != 0 {
		times := []unix.Timespec{unix.NsecToTimespec(entry.ATime), unix.NsecToTimespec(entry.MTime)}
		flags := 0
		if entry.Kind == "symlink" {
			flags = unix.AT_SYMLINK_NOFOLLOW
		}
		if err := unix.UtimesNanoAt(unix.AT_FDCWD, target, times, flags); err != nil {
			return err
		}
	}
	return nil
}

func readXattrs(path string) ([]Xattr, error) {
	size, err := unix.Llistxattr(path, nil)
	if err == unix.ENOTSUP {
		return nil, nil
	}
	if err != nil || size == 0 {
		return nil, err
	}
	buffer := make([]byte, size)
	size, err = unix.Llistxattr(path, buffer)
	if err != nil {
		return nil, err
	}
	var result []Xattr
	for _, raw := range strings.Split(string(buffer[:size]), "\x00") {
		if raw == "" {
			continue
		}
		valueSize, err := unix.Lgetxattr(path, raw, nil)
		if err != nil {
			return nil, err
		}
		value := make([]byte, valueSize)
		if valueSize != 0 {
			if _, err := unix.Lgetxattr(path, raw, value); err != nil {
				return nil, err
			}
		}
		result = append(result, Xattr{Name: raw, Value: value})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func writeXattrs(path string, attributes []Xattr) error {
	for _, attribute := range attributes {
		if err := unix.Lsetxattr(path, attribute.Name, attribute.Value, 0); err != nil {
			return fmt.Errorf("restore xattr %s on %s: %w", attribute.Name, path, err)
		}
	}
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
