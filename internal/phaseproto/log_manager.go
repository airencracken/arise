package phaseproto

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PackageLogOptions describes the durable Portage-compatible log layout.
type PackageLogOptions struct {
	Root, TempDir, Category, PF string
	Split, Compress             bool
	Now                         time.Time
	FilterCommand               []string
}

// PackageLog is a fail-closed per-package durable event sink.
type PackageLog struct {
	path, canonical string
	activeMarker    string
	file            *os.File
	writer          io.Writer
	filterInput     io.WriteCloser
	filter          *exec.Cmd
	closed          bool
}

func NewPackageLog(options PackageLogOptions) (*PackageLog, error) {
	if options.Root == "" || options.TempDir == "" || options.Category == "" || options.PF == "" ||
		strings.ContainsAny(options.Category+options.PF, "/\\\x00") {
		return nil, fmt.Errorf("phase log: incomplete or unsafe package log options")
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	timestamp := options.Now.UTC().Format("20060102-150405")
	directory := options.Root
	if options.Split {
		directory = filepath.Join(options.Root, "build", options.Category)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("phase log: create directory: %w", err)
	}
	name := options.Category + ":" + options.PF + ":" + timestamp + ".log"
	if options.Split {
		name = options.PF + ":" + timestamp + ".log"
	}
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("phase log: reserve %s: %w", path, err)
	}
	canonical := filepath.Join(options.TempDir, "build.log")
	if err := os.MkdirAll(options.TempDir, 0o700); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("phase log: create T: %w", err)
	}
	if err := os.Symlink(path, canonical); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("phase log: create canonical build.log: %w", err)
	}
	activeMarker := path + ".active"
	marker, err := os.OpenFile(activeMarker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("phase log: create active marker: %w", err)
	}
	if _, err = io.WriteString(marker, path+"\n"); err == nil {
		err = marker.Sync()
	}
	if closeErr := marker.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("phase log: persist active marker: %w", err)
	}
	result := &PackageLog{path: path, canonical: canonical, activeMarker: activeMarker, file: file, writer: file}
	if len(options.FilterCommand) != 0 {
		command := exec.Command(options.FilterCommand[0], options.FilterCommand[1:]...)
		input, pipeErr := command.StdinPipe()
		if pipeErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("phase log: create filter input: %w", pipeErr)
		}
		command.Stdout, command.Stderr = file, file
		if startErr := command.Start(); startErr != nil {
			_ = input.Close()
			_ = file.Close()
			return nil, fmt.Errorf("phase log: start filter: %w", startErr)
		}
		result.writer, result.filterInput, result.filter = input, input, command
	}
	return result, nil
}

func (l *PackageLog) Path() string          { return l.path }
func (l *PackageLog) CanonicalPath() string { return l.canonical }

func (l *PackageLog) WriteRecord(sequence uint64, job, phase, kind, stream, message string) error {
	if l == nil || l.closed || l.file == nil {
		return fmt.Errorf("phase log: write after finalization")
	}
	line := fmt.Sprintf("sequence=%d job=%q phase=%q kind=%q stream=%q message=%q\n", sequence, job, phase, kind, stream, message)
	if _, err := io.WriteString(l.writer, line); err != nil {
		return fmt.Errorf("phase log: write %s: %w", l.path, err)
	}
	return nil
}

// Sync establishes a durable worker-batch boundary without forcing every
// individual log line through a synchronous filesystem commit.
func (l *PackageLog) Sync() error {
	if l == nil || l.closed || l.file == nil {
		return fmt.Errorf("phase log: sync after finalization")
	}
	if l.filter != nil {
		return nil
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("phase log: sync %s: %w", l.path, err)
	}
	return nil
}

// Finalize syncs and closes the log. Compression is atomic and updates both
// the durable path and T/build.log only after the gzip stream is durable.
func (l *PackageLog) Finalize(compress bool) error {
	if l == nil || l.closed || l.file == nil {
		return fmt.Errorf("phase log: duplicate finalization")
	}
	if l.filterInput != nil {
		if err := l.filterInput.Close(); err != nil {
			return fmt.Errorf("phase log: close filter input: %w", err)
		}
		if err := l.filter.Wait(); err != nil {
			return fmt.Errorf("phase log: filter failed for %s: %w", l.path, err)
		}
		l.filterInput = nil
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("phase log: sync %s: %w", l.path, err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("phase log: close %s: %w", l.path, err)
	}
	l.closed = true
	if !compress {
		if err := os.Remove(l.activeMarker); err != nil {
			return fmt.Errorf("phase log: clear active marker: %w", err)
		}
		return nil
	}
	source, err := os.Open(l.path)
	if err != nil {
		return fmt.Errorf("phase log: reopen for compression: %w", err)
	}
	defer source.Close()
	temporary := l.path + ".gz.tmp"
	destination, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("phase log: create compressed log: %w", err)
	}
	zw := gzip.NewWriter(destination)
	if _, err = io.Copy(zw, source); err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = destination.Sync()
	}
	if closeErr := destination.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("phase log: compress %s: %w", l.path, err)
	}
	compressed := l.path + ".gz"
	if err := os.Rename(temporary, compressed); err != nil {
		return fmt.Errorf("phase log: publish compressed log: %w", err)
	}
	if err := os.Remove(l.path); err != nil {
		return fmt.Errorf("phase log: remove uncompressed log: %w", err)
	}
	if err := os.Remove(l.canonical); err != nil {
		return fmt.Errorf("phase log: replace canonical log: %w", err)
	}
	if err := os.Symlink(compressed, l.canonical); err != nil {
		return fmt.Errorf("phase log: link compressed canonical log: %w", err)
	}
	l.path = compressed
	if err := os.Remove(l.activeMarker); err != nil {
		return fmt.Errorf("phase log: clear active marker: %w", err)
	}
	return nil
}

// InterruptedPackageLogs returns durable log paths whose active marker survived
// process death or cancellation before successful finalization.
func InterruptedPackageLogs(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log.active") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		logPath := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(logPath) {
			return fmt.Errorf("phase log: unsafe active marker %s", path)
		}
		relative, err := filepath.Rel(root, logPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("phase log: active marker %s escapes log root", path)
		}
		if _, err := os.Stat(logPath); err != nil {
			return fmt.Errorf("phase log: active marker %s references unavailable log: %w", path, err)
		}
		paths = append(paths, logPath)
		return nil
	})
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("phase log: scan interrupted logs: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}
