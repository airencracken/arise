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

// Discover recursively finds pending updates, removes superseded candidates,
// and returns one newest candidate per target in stable target order.
func Discover(opts Options) ([]Candidate, error) {
	root, err := cleanRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	byCurrent := make(map[string]Candidate)
	var superseded []string
	for _, protected := range opts.Protect {
		scan, basename, err := rootedScanPath(root, protected)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(scan)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat protected path %s: %w", scan, err)
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
					superseded = append(superseded, old.New)
					byCurrent[current] = candidate
				} else {
					superseded = append(superseded, candidate.New)
				}
			} else {
				byCurrent[current] = candidate
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("scan protected path %s: %w", scan, walkErr)
		}
	}
	for _, path := range superseded {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove superseded candidate %s: %w", path, err)
		}
	}
	out := make([]Candidate, 0, len(byCurrent))
	for _, candidate := range byCurrent {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Current < out[j].Current })
	return out, nil
}

// Run discovers and processes a dispatch-conf session.
func Run(ctx context.Context, opts Options) (Result, error) {
	opts = normalize(opts)
	candidates, err := Discover(opts)
	if err != nil {
		return Result{}, err
	}
	result := Result{Discovered: len(candidates)}
	if err := runHooks(ctx, opts.HookDir, "pre-session"); err != nil {
		return result, err
	}
	defer func() {
		_ = runHooks(context.Background(), opts.HookDir, "post-session")
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
		for {
			_ = showDiff(ctx, opts, candidate.Current, selected)
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
					_ = showDiff(ctx, opts, candidate.New, merged)
				}
			case 'z':
				if err := removeCandidateAndMerge(candidate.New, merged); err != nil {
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
				result.Updated++
				emit(opts, Event{Kind: "update", Candidate: candidate})
				goto next
			}
		}
	next:
	}
	return result, nil
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
		dist := archivePath(candidate, opts) + ".dist"
		if same, err := sameContent(candidate.Current, dist); err == nil && same {
			return "update", nil
		}
	}
	if opts.IgnorePreviouslyMerged {
		dist := archivePath(candidate, opts) + ".dist"
		if same, err := sameContent(candidate.New, dist); err == nil && same {
			return "zap", nil
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

func archivePath(candidate Candidate, opts Options) string {
	rel := strings.TrimPrefix(candidate.Current, filepath.Clean(opts.Root))
	return filepath.Join(opts.ArchiveDir, strings.TrimPrefix(rel, string(filepath.Separator)))
}

func archive(candidate Candidate, opts Options) error {
	path := archivePath(candidate, opts)
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
	if err := copyFile(candidate.Current, path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("archive current %s: %w", candidate.Current, err)
	}
	if err := copyFile(candidate.New, path+".dist.new"); err != nil {
		return fmt.Errorf("archive new %s: %w", candidate.New, err)
	}
	return nil
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
	return os.MkdirAll(cleanParent, 0o700)
}

func rotate(path string) error {
	if _, err := os.Lstat(path + ".9"); err == nil {
		if err := os.Remove(path + ".9"); err != nil {
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
		return os.Rename(path, path+".1")
	}
	return nil
}

func copyFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	_ = os.Remove(destination)
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(target, destination)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file or symlink", source)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	return os.Chtimes(destination, info.ModTime(), info.ModTime())
}

func copyMetadata(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	return os.Chmod(destination, info.Mode().Perm())
}

func postProcessArchive(candidate Candidate, opts Options) error {
	path := archivePath(candidate, opts)
	if _, err := os.Lstat(path + ".dist.new"); err == nil {
		if _, err := os.Lstat(path + ".dist"); err == nil {
			if err := rotate(path + ".dist"); err != nil {
				return err
			}
		}
		return os.Rename(path+".dist.new", path+".dist")
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
	if err := runHooks(ctx, opts.HookDir, "post-update", candidate.Current); err != nil {
		return err
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
	parts, err := commandParts(opts.DiffCommand, oldPath, newPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdout, cmd.Stderr = opts.Output, opts.Error
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return err
}

func runMerge(ctx context.Context, command, output, oldPath, newPath string) error {
	parts, err := commandParts(command, output, oldPath, newPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return err
}

func runEditor(ctx context.Context, editor, path string) error {
	parts := strings.Fields(editor)
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
