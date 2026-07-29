// Package dispatchconf manages Portage protected configuration updates.
package dispatchconf

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	shlex "github.com/anmitsu/go-shlex"
)

var candidateName = regexp.MustCompile(`^\._cfg([0-9]{4})_(.+)$`)

// Candidate is an immutable snapshot of a pending update discovered on disk.
type Candidate struct {
	Current  string `json:"current"`
	New      string `json:"new"`
	Sequence int    `json:"sequence"`
	Masked   bool   `json:"masked"`
}

// Event describes an observable session action.
type Event struct {
	Kind      string    `json:"kind"`
	Candidate Candidate `json:"candidate"`
}

// Options configures a session. Paths in Protect and Mask are root-relative.
type Options struct {
	Root                   string
	Protect                []string
	Mask                   []string
	ArchiveDir             string
	HookDir                string
	DiffCommand            string
	MergeCommand           string
	Editor                 string
	FrozenFiles            []string
	ReplaceCVS             bool
	ReplaceWSComments      bool
	ReplaceUnmodified      bool
	IgnorePreviouslyMerged bool
	ClearScreen            bool
	Color                  bool
	Input                  io.Reader
	Output                 io.Writer
	Error                  io.Writer
	OnEvent                func(Event)
}

// Result summarizes a completed or deliberately stopped session.
type Result struct {
	Discovered int  `json:"discovered"`
	Updated    int  `json:"updated"`
	Zapped     int  `json:"zapped"`
	Skipped    int  `json:"skipped"`
	Automatic  int  `json:"automatic"`
	Quit       bool `json:"quit"`
}

// DefaultOptions returns the installed dispatch-conf-compatible defaults.
func DefaultOptions() Options {
	return Options{
		Root: "/", ArchiveDir: "/etc/config-archive",
		HookDir:      "/etc/portage/conf-update.d",
		DiffCommand:  "diff -Nu %s %s",
		MergeCommand: "sdiff --suppress-common-lines --output=%s %s %s",
		Editor:       "nano", ReplaceCVS: true,
		Input: os.Stdin, Output: os.Stdout, Error: os.Stderr,
	}
}

type discovery struct {
	candidates []Candidate
	superseded map[string][]string
}

// Discover recursively finds pending updates without mutating them and returns
// one newest candidate per target in Portage dispatch-conf's stable candidate
// path order.
func Discover(opts Options) ([]Candidate, error) {
	found, err := discover(opts)
	if err != nil {
		return nil, err
	}
	return found.candidates, nil
}

func discover(opts Options) (discovery, error) {
	root, err := cleanRoot(opts.Root)
	if err != nil {
		return discovery{}, err
	}
	byCurrent := make(map[string]Candidate)
	superseded := make(map[string][]string)
	for _, protected := range opts.Protect {
		scan, basename, err := rootedScanPath(root, protected)
		if err != nil {
			return discovery{}, err
		}
		info, err := os.Stat(scan)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return discovery{}, fmt.Errorf("stat protected path %s: %w", scan, err)
		}
		if !info.IsDir() {
			scan = filepath.Dir(scan)
		}
		walkErr := filepath.WalkDir(scan, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != scan && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				if basename != "" && path != scan {
					return filepath.SkipDir
				}
				return nil
			}
			match := candidateName.FindStringSubmatch(entry.Name())
			if match == nil || (basename != "" && match[2] != basename) {
				return nil
			}
			seq, _ := strconv.Atoi(match[1])
			current := filepath.Join(filepath.Dir(path), match[2])
			candidate := Candidate{
				Current: current, New: path, Sequence: seq,
				Masked: isMasked(root, current, opts.Mask),
			}
			if old, exists := byCurrent[current]; exists {
				if candidate.New > old.New {
					superseded[current] = append(superseded[current], old.New)
					byCurrent[current] = candidate
				} else {
					superseded[current] = append(superseded[current], candidate.New)
				}
			} else {
				byCurrent[current] = candidate
			}
			return nil
		})
		if walkErr != nil {
			return discovery{}, fmt.Errorf("scan protected path %s: %w", scan, walkErr)
		}
	}
	out := make([]Candidate, 0, len(byCurrent))
	for _, candidate := range byCurrent {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].New < out[j].New })
	return discovery{candidates: out, superseded: superseded}, nil
}

// Run discovers and processes a dispatch-conf session.
func Run(ctx context.Context, opts Options) (result Result, runErr error) {
	opts = normalize(opts)
	if err := validateOptions(opts); err != nil {
		return Result{}, err
	}
	found, err := discover(opts)
	if err != nil {
		return Result{}, err
	}
	candidates := found.candidates
	result = Result{Discovered: len(candidates)}
	if err := runHooks(ctx, opts.HookDir, "pre-session"); err != nil {
		return result, err
	}
	defer func() {
		if hookErr := runHooks(context.WithoutCancel(ctx), opts.HookDir, "post-session"); hookErr != nil {
			runErr = errors.Join(runErr, hookErr)
		}
	}()
	reader := bufio.NewReader(opts.Input)
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := archive(candidate, opts); err != nil {
			return result, err
		}
		automatic, err := automaticDecision(candidate, opts)
		if err != nil {
			return result, err
		}
		if automatic != "" {
			if automatic == "update" {
				if err := replace(ctx, candidate.New, candidate, opts); err != nil {
					return result, err
				}
				if err := postProcessArchive(candidate, opts); err != nil {
					return result, err
				}
				result.Updated++
			} else {
				if err := os.Remove(candidate.New); err != nil {
					return result, err
				}
				if automatic == "identical" {
					if err := postProcessArchive(candidate, opts); err != nil {
						return result, err
					}
				}
				result.Zapped++
			}
			if err := removePaths(found.superseded[candidate.Current]); err != nil {
				return result, err
			}
			result.Automatic++
			kind := automatic
			if kind == "identical" || kind == "frozen" {
				kind = "zap"
			}
			emit(opts, Event{Kind: "automatic-" + kind, Candidate: candidate})
			continue
		}
		selected := candidate.New
		merged := mergedPath(candidate.New)
		if _, err := os.Lstat(merged); err == nil {
			selected = merged
		}
		for {
			clearScreen(opts)
			if err := showDiff(ctx, opts, candidate.Current, selected); err != nil {
				return result, fmt.Errorf("show diff: %w", err)
			}
			fmt.Fprintf(opts.Output, "\n>> (%d of %d) -- %s\n", index+1, len(candidates), candidate.Current)
			fmt.Fprint(opts.Output, ">> q quit, h help, n next, e edit-new, z zap-new, u use-new\n   m merge, t toggle-merge, l look-merge: ")
			decision, readErr := readDecision(reader)
			if readErr != nil {
				return result, readErr
			}
			fmt.Fprintln(opts.Output)
			switch decision {
			case 'q':
				result.Quit = true
				emit(opts, Event{Kind: "quit", Candidate: candidate})
				return result, nil
			case 'h':
				printHelp(opts.Output)
			case 'n':
				result.Skipped++
				emit(opts, Event{Kind: "skip", Candidate: candidate})
				goto next
			case 'e':
				if err := runEditor(ctx, opts.Editor, selected); err != nil {
					fmt.Fprintf(opts.Error, "dispatch-conf: editor: %v\n", err)
				}
			case 'm':
				if err := runMerge(ctx, opts.MergeCommand, merged, candidate.Current, selected); err != nil {
					fmt.Fprintf(opts.Error, "dispatch-conf: merge: %v\n", err)
					continue
				}
				if err := copyMetadata(candidate.New, merged); err != nil {
					return result, err
				}
				selected = merged
			case 't':
				if selected == merged {
					selected = candidate.New
				} else if _, err := os.Lstat(merged); err == nil {
					selected = merged
				}
			case 'l':
				if _, err := os.Lstat(merged); err == nil {
					if err := showDiff(ctx, opts, candidate.New, merged); err != nil {
						return result, fmt.Errorf("show merged diff: %w", err)
					}
				}
			case 'z':
				if err := removeCandidateAndMerge(candidate.New, merged); err != nil {
					return result, err
				}
				if err := removePaths(found.superseded[candidate.Current]); err != nil {
					return result, err
				}
				result.Zapped++
				emit(opts, Event{Kind: "zap", Candidate: candidate})
				goto next
			case 'u':
				if err := replace(ctx, selected, candidate, opts); err != nil {
					return result, err
				}
				if selected == merged {
					if err := os.Remove(candidate.New); err != nil && !errors.Is(err, fs.ErrNotExist) {
						return result, err
					}
				}
				_ = os.Remove(merged)
				if err := postProcessArchive(candidate, opts); err != nil {
					return result, err
				}
				if err := removePaths(found.superseded[candidate.Current]); err != nil {
					return result, err
				}
				result.Updated++
				emit(opts, Event{Kind: "update", Candidate: candidate})
				goto next
			}
		}
	next:
	}
	return result, runErr
}

func normalize(opts Options) Options {
	defaults := DefaultOptions()
	if opts.Root == "" {
		opts.Root = defaults.Root
	}
	if opts.ArchiveDir == "" {
		opts.ArchiveDir = rooted(opts.Root, defaults.ArchiveDir)
	}
	if opts.HookDir == "" {
		opts.HookDir = rooted(opts.Root, defaults.HookDir)
	}
	if opts.DiffCommand == "" {
		opts.DiffCommand = defaults.DiffCommand
	}
	if opts.MergeCommand == "" {
		opts.MergeCommand = defaults.MergeCommand
	}
	if opts.Editor == "" {
		opts.Editor = defaults.Editor
	}
	if opts.Input == nil {
		opts.Input = defaults.Input
	}
	if opts.Output == nil {
		opts.Output = defaults.Output
	}
	if opts.Error == nil {
		opts.Error = defaults.Error
	}
	return opts
}

func cleanRoot(root string) (string, error) {
	if root == "" {
		root = "/"
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("root must be absolute: %q", root)
	}
	return filepath.Clean(root), nil
}

func rooted(root, path string) string {
	if root == "" {
		root = "/"
	}
	if filepath.IsAbs(path) {
		return filepath.Join(root, strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)))
	}
	return filepath.Join(root, path)
}

func rootedScanPath(root, protected string) (string, string, error) {
	if protected == "" {
		return "", "", errors.New("empty CONFIG_PROTECT path")
	}
	path := rooted(root, protected)
	if !pathWithin(root, path) {
		return "", "", fmt.Errorf("CONFIG_PROTECT path escapes root: %q", protected)
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return path, filepath.Base(path), nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", err
	}
	if errors.Is(err, fs.ErrNotExist) {
		parent := filepath.Dir(path)
		if parentInfo, parentErr := os.Stat(parent); parentErr == nil && parentInfo.IsDir() {
			return path, filepath.Base(path), nil
		}
	}
	return path, "", nil
}

func isMasked(root, current string, masks []string) bool {
	for _, mask := range masks {
		path := rooted(root, mask)
		if current == path || strings.HasPrefix(current, path+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func sameContent(a, b string) (bool, error) {
	leftInfo, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	if leftInfo.Mode().Type() != rightInfo.Mode().Type() {
		return false, nil
	}
	if leftInfo.Mode()&os.ModeSymlink != 0 {
		left, err := os.Readlink(a)
		if err != nil {
			return false, err
		}
		right, err := os.Readlink(b)
		return left == right, err
	}
	if !leftInfo.Mode().IsRegular() {
		return false, nil
	}
	left, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	right, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(left, right), nil
}

func automaticDecision(candidate Candidate, opts Options) (string, error) {
	same, err := sameContent(candidate.Current, candidate.New)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if same {
		return "identical", nil
	}
	for _, frozen := range opts.FrozenFiles {
		if candidate.Current == rooted(opts.Root, frozen) {
			return "frozen", nil
		}
	}
	if candidate.Masked {
		return "update", nil
	}
	currentInfo, currentErr := os.Lstat(candidate.Current)
	newInfo, newErr := os.Lstat(candidate.New)
	if currentErr != nil || newErr != nil {
		return "", errors.Join(currentErr, newErr)
	}
	if !currentInfo.Mode().IsRegular() || !newInfo.Mode().IsRegular() {
		return "", nil
	}
	old, err := os.ReadFile(candidate.Current)
	if err != nil {
		return "", err
	}
	newData, err := os.ReadFile(candidate.New)
	if err != nil {
		return "", err
	}
	if opts.ReplaceCVS && equivalentCVS(old, newData) {
		return "update", nil
	}
	if opts.ReplaceWSComments && equivalentWSComments(old, newData) {
		return "update", nil
	}
	if opts.ReplaceUnmodified {
		if archive, pathErr := archivePath(candidate, opts); pathErr == nil {
			if same, compareErr := sameContent(candidate.Current, archive+".dist"); compareErr == nil && same {
				return "update", nil
			}
		}
	}
	if opts.IgnorePreviouslyMerged {
		if archive, pathErr := archivePath(candidate, opts); pathErr == nil {
			if same, compareErr := sameContent(candidate.New, archive+".dist"); compareErr == nil && same {
				return "zap", nil
			}
		}
	}
	return "", nil
}

func equivalentCVS(a, b []byte) bool {
	clean := func(data []byte) []byte {
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "# $Header:") {
				lines[i] = ""
			}
		}
		return []byte(strings.Join(lines, "\n"))
	}
	return bytes.Equal(clean(a), clean(b))
}

func equivalentWSComments(a, b []byte) bool {
	clean := func(data []byte) string {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	return clean(a) == clean(b)
}

func archivePath(candidate Candidate, opts Options) (string, error) {
	root, err := cleanRoot(opts.Root)
	if err != nil {
		return "", err
	}
	if err := validateCandidate(candidate, root); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, candidate.Current)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("candidate current escapes root: %s", candidate.Current)
	}
	archiveDir := filepath.Clean(opts.ArchiveDir)
	if !filepath.IsAbs(archiveDir) {
		return "", fmt.Errorf("archive directory must be absolute: %q", opts.ArchiveDir)
	}
	path := filepath.Join(archiveDir, rel)
	if !pathWithin(archiveDir, path) {
		return "", fmt.Errorf("archive path escapes archive directory")
	}
	return path, nil
}

func archive(candidate Candidate, opts Options) error {
	path, err := archivePath(candidate, opts)
	if err != nil {
		return err
	}
	info, err := os.Lstat(candidate.New)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("candidate has unsupported object type %s", info.Mode().Type())
	}
	if err := ensureArchiveParent(filepath.Dir(path), opts.ArchiveDir); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		same, compareErr := sameContent(candidate.Current, path)
		if compareErr != nil || !same {
			if err := rotate(path); err != nil {
				return err
			}
		}
	}
	if currentInfo, statErr := os.Lstat(candidate.Current); statErr == nil {
		if currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 {
			if err := atomicCopy(candidate.Current, path); err != nil {
				return fmt.Errorf("archive current %s: %w", candidate.Current, err)
			}
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("archive current %s: %w", candidate.Current, statErr)
	}
	if err := prepareThreeWayMerge(candidate, path); err != nil {
		return err
	}
	if err := atomicCopy(candidate.New, path+".dist.new"); err != nil {
		return fmt.Errorf("archive new %s: %w", candidate.New, err)
	}
	return nil
}

func prepareThreeWayMerge(candidate Candidate, archive string) error {
	for _, path := range []string{candidate.Current, candidate.New, archive + ".dist"} {
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	cmd := exec.Command("diff3", "-mE", candidate.Current, archive+".dist", candidate.New)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) || errors.Is(err, exec.ErrNotFound) {
			_ = os.Remove(mergedPath(candidate.New))
			return nil
		}
		return fmt.Errorf("automatic three-way merge: %w", err)
	}
	return atomicWriteFrom(candidate.New, mergedPath(candidate.New), output)
}

func atomicWriteFrom(metadataSource, destination string, content []byte) error {
	info, err := os.Stat(metadataSource)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	temp, err := os.CreateTemp(parent, "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Chown(tempPath, int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	cleanup = false
	return syncDir(parent)
}

func ensureArchiveParent(parent, boundary string) error {
	cleanBoundary := filepath.Clean(boundary)
	cleanParent := filepath.Clean(parent)
	if cleanParent != cleanBoundary && !strings.HasPrefix(cleanParent, cleanBoundary+string(filepath.Separator)) {
		return fmt.Errorf("archive path escapes archive directory")
	}
	for path := cleanParent; ; path = filepath.Dir(path) {
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			if err := rotate(path); err != nil {
				return err
			}
		}
		if path == cleanBoundary {
			break
		}
	}
	if err := os.MkdirAll(cleanParent, 0o700); err != nil {
		return err
	}
	for path := cleanBoundary; ; {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe archive directory ancestor %s", path)
		}
		if path == cleanParent {
			break
		}
		rel, _ := filepath.Rel(cleanBoundary, cleanParent)
		next := strings.Split(rel, string(filepath.Separator))[0]
		path = filepath.Join(path, next)
		cleanBoundary = path
	}
	return nil
}

func rotate(path string) error {
	if _, err := os.Lstat(path + ".9"); err == nil {
		info, statErr := os.Lstat(path + ".9")
		if statErr != nil {
			return statErr
		}
		if info.IsDir() {
			placeholder, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".9.displaced-")
			if err != nil {
				return err
			}
			preserved := placeholder.Name()
			if err := placeholder.Close(); err != nil {
				return err
			}
			if err := os.Remove(preserved); err != nil {
				return err
			}
			if err := os.Rename(path+".9", preserved); err != nil {
				return err
			}
		} else if err := os.Remove(path + ".9"); err != nil {
			return err
		}
	}
	for i := 8; i >= 1; i-- {
		from, to := fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Lstat(from); err == nil {
			if err := os.Rename(from, to); err != nil {
				return err
			}
		}
	}
	if _, err := os.Lstat(path); err == nil {
		if err := os.Rename(path, path+".1"); err != nil {
			return err
		}
		return syncDir(filepath.Dir(path))
	}
	return nil
}

func atomicCopy(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	temp, err := os.CreateTemp(parent, "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if closeErr := temp.Close(); closeErr != nil {
		_ = os.Remove(tempPath)
		return closeErr
	}
	if err := os.Remove(tempPath); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, tempPath); err != nil {
			return err
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			if err := os.Lchown(tempPath, int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
				return err
			}
		}
	} else {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file or symlink", source)
		}
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
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
		if closeErr != nil {
			return closeErr
		}
		if err := os.Chmod(tempPath, info.Mode().Perm()); err != nil {
			return err
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			if err := os.Chown(tempPath, int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
				return err
			}
		}
		if err := os.Chtimes(tempPath, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	cleanup = false
	return syncDir(parent)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func validateCandidate(candidate Candidate, root string) error {
	if !filepath.IsAbs(candidate.Current) || !filepath.IsAbs(candidate.New) {
		return errors.New("candidate paths must be absolute")
	}
	if !pathWithin(root, candidate.Current) || !pathWithin(root, candidate.New) {
		return errors.New("candidate path escapes root")
	}
	if filepath.Dir(candidate.Current) != filepath.Dir(candidate.New) {
		return errors.New("candidate current and new paths have different parents")
	}
	match := candidateName.FindStringSubmatch(filepath.Base(candidate.New))
	if match == nil || match[2] != filepath.Base(candidate.Current) {
		return errors.New("candidate name does not match current path")
	}
	return nil
}

func copyMetadata(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Chown(destination, int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
	}
	return os.Chtimes(destination, info.ModTime(), info.ModTime())
}

func postProcessArchive(candidate Candidate, opts Options) error {
	path, err := archivePath(candidate, opts)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path + ".dist.new"); err == nil {
		if _, err := os.Lstat(path + ".dist"); err == nil {
			if err := rotate(path + ".dist"); err != nil {
				return err
			}
		}
		if err := os.Rename(path+".dist.new", path+".dist"); err != nil {
			return err
		}
		return syncDir(filepath.Dir(path))
	}
	return nil
}

func replace(ctx context.Context, source string, candidate Candidate, opts Options) error {
	if err := runHooks(ctx, opts.HookDir, "pre-update", candidate.Current); err != nil {
		return err
	}
	if err := os.Rename(source, candidate.Current); err != nil {
		return fmt.Errorf("rename %s to %s: %w", source, candidate.Current, err)
	}
	if err := syncDir(filepath.Dir(candidate.Current)); err != nil {
		return fmt.Errorf("sync updated config directory: %w", err)
	}
	if err := runHooks(ctx, opts.HookDir, "post-update", candidate.Current); err != nil {
		return fmt.Errorf("updated config committed; post-update hook failed: %w", err)
	}
	return nil
}

func runHooks(ctx context.Context, dir, kind string, args ...string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read hook directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o111 == 0 {
			continue
		}
		commandArgs := append([]string{kind}, args...)
		cmd := exec.CommandContext(ctx, path, commandArgs...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("hook %s %s: %w: %s", path, kind, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func commandParts(command string, replacements ...string) ([]string, error) {
	if got := strings.Count(command, "%s"); got != len(replacements) {
		return nil, fmt.Errorf("command has %d %%s placeholders, want %d", got, len(replacements))
	}
	for _, replacement := range replacements {
		command = strings.Replace(command, "%s", replacement, 1)
	}
	parts, err := shlex.Split(command, true)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, errors.New("empty command")
	}
	return parts, nil
}

func showDiff(ctx context.Context, opts Options, oldPath, newPath string) error {
	left, right, cleanup, err := mixedDiffOperands(oldPath, newPath)
	if err != nil {
		return err
	}
	defer cleanup()
	parts, err := commandParts(opts.DiffCommand, left, right)
	if err != nil {
		return err
	}
	parts = colorDiffCommand(parts, opts.Color)
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdout, cmd.Stderr = opts.Output, opts.Error
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return err
}

func clearScreen(opts Options) {
	if opts.ClearScreen {
		fmt.Fprint(opts.Output, "\033[H\033[2J")
	}
}

func colorDiffCommand(parts []string, enabled bool) []string {
	if !enabled || len(parts) == 0 || filepath.Base(parts[0]) != "diff" {
		return parts
	}
	for _, part := range parts[1:] {
		if part == "--color" || strings.HasPrefix(part, "--color=") {
			return parts
		}
	}
	colored := make([]string, 0, len(parts)+1)
	colored = append(colored, parts[0], "--color=always")
	return append(colored, parts[1:]...)
}

func mixedDiffOperands(left, right string) (string, string, func(), error) {
	leftInfo, leftErr := os.Lstat(left)
	rightInfo, rightErr := os.Lstat(right)
	if leftErr == nil && rightErr == nil &&
		leftInfo.Mode()&os.ModeSymlink != 0 && rightInfo.Mode().IsRegular() {
		if followed, err := os.Stat(left); err == nil && followed.Mode().IsRegular() {
			return right, right, func() {}, nil
		}
	}
	if (leftErr == nil && leftInfo.Mode().IsRegular()) && (rightErr == nil && rightInfo.Mode().IsRegular()) {
		return left, right, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "arise-dispatch-diff-")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	render := func(index int, path string, info os.FileInfo, statErr error) (string, error) {
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return "", statErr
		}
		if statErr == nil && info.Mode().IsRegular() {
			return path, nil
		}
		content := "/dev/null\n"
		if statErr == nil {
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return "", err
				}
				content = fmt.Sprintf("SYM: %s -> %s\n", path, target)
			case info.IsDir():
				content = fmt.Sprintf("DIR: %s\n", path)
			case info.Mode()&os.ModeNamedPipe != 0:
				content = fmt.Sprintf("FIF: %s\n", path)
			default:
				content = fmt.Sprintf("DEV: %s\n", path)
			}
		}
		rendered := filepath.Join(dir, strconv.Itoa(index))
		if err := os.WriteFile(rendered, []byte(content), 0o600); err != nil {
			return "", err
		}
		return rendered, nil
	}
	renderedLeft, err := render(0, left, leftInfo, leftErr)
	if err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	renderedRight, err := render(1, right, rightInfo, rightErr)
	if err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	return renderedLeft, renderedRight, cleanup, nil
}

func runMerge(ctx context.Context, command, output, oldPath, newPath string) error {
	temp, err := os.CreateTemp(filepath.Dir(output), "."+filepath.Base(output)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(tempPath); err != nil {
		return err
	}
	defer os.Remove(tempPath)
	parts, err := commandParts(command, tempPath, oldPath, newPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Lstat(tempPath); err != nil {
		return fmt.Errorf("merge command did not create output: %w", err)
	}
	if err := os.Rename(tempPath, output); err != nil {
		return err
	}
	return syncDir(filepath.Dir(output))
}

func runEditor(ctx context.Context, editor, path string) error {
	parts, err := shlex.Split(editor, true)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return errors.New("empty editor")
	}
	parts = append(parts, path)
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func readDecision(reader *bufio.Reader) (byte, error) {
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if strings.ContainsRune("qhtnmlezu", rune(value)) {
			return value, nil
		}
	}
}

func mergedPath(path string) string { return strings.Replace(path, "._cfg", "._mrg", 1) }

func removeCandidateAndMerge(candidate, merged string) error {
	if err := os.Remove(candidate); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(merged); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func removePaths(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove superseded candidate %s: %w", path, err)
		}
	}
	return nil
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "\n  u -- update current config with new config and continue")
	fmt.Fprintln(out, "  z -- zap (delete) new config and continue")
	fmt.Fprintln(out, "  n -- skip to next config, leave all intact")
	fmt.Fprintln(out, "  e -- edit new config")
	fmt.Fprintln(out, "  m -- interactively merge current and new configs")
	fmt.Fprintln(out, "  l -- look at diff between pre-merged and merged configs")
	fmt.Fprintln(out, "  t -- toggle new config between merged and pre-merged state")
	fmt.Fprintln(out, "  h -- this screen")
	fmt.Fprintln(out, "  q -- quit")
}

func emit(opts Options, event Event) {
	if opts.OnEvent != nil {
		opts.OnEvent(event)
	}
}

func validateOptions(opts Options) error {
	if _, err := cleanRoot(opts.Root); err != nil {
		return err
	}
	if !filepath.IsAbs(opts.ArchiveDir) {
		return fmt.Errorf("archive directory must be absolute: %q", opts.ArchiveDir)
	}
	if _, err := commandParts(opts.DiffCommand, "old", "new"); err != nil {
		return fmt.Errorf("invalid diff command: %w", err)
	}
	if _, err := commandParts(opts.MergeCommand, "output", "old", "new"); err != nil {
		return fmt.Errorf("invalid merge command: %w", err)
	}
	editor, err := shlex.Split(opts.Editor, true)
	if err != nil {
		return fmt.Errorf("invalid editor command: %w", err)
	}
	if len(editor) == 0 {
		return errors.New("invalid editor command: empty command")
	}
	return nil
}
