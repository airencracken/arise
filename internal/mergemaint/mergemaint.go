// Package mergemaint audits and cleans interrupted Portage VDB merge entries.
package mergemaint

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/journal"
)

const Identifier = "-MERGING-"

var removeTree = func(operation *journal.Journal, path string) error {
	return operation.RemoveTree(path)
}

type Failed struct {
	Path      string `json:"path"`
	Entry     string `json:"entry"`
	CPV       string `json:"cpv"`
	Atom      string `json:"atom"`
	MTimeUnix int64  `json:"mtime_unix"`
	Tracked   bool   `json:"tracked"`
	Present   bool   `json:"present"`
}

type Report struct {
	Failed []Failed `json:"failed"`
}

func Check(vdbRoot, trackingPath string) (Report, error) {
	byEntry := map[string]Failed{}
	categories, err := os.ReadDir(vdbRoot)
	if err != nil && !os.IsNotExist(err) {
		return Report{}, fmt.Errorf("merges: read VDB: %w", err)
	}
	for _, category := range categories {
		if !category.IsDir() || strings.Contains(category.Name(), "/") {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(vdbRoot, category.Name()))
		if err != nil {
			return Report{}, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.Contains(entry.Name(), Identifier) {
				continue
			}
			relative := category.Name() + "/" + entry.Name()
			failed, err := parseFailed(relative)
			if err != nil {
				return Report{}, err
			}
			info, err := entry.Info()
			if err != nil {
				return Report{}, err
			}
			failed.Path = filepath.Join(vdbRoot, category.Name(), entry.Name())
			failed.MTimeUnix, failed.Present = info.ModTime().Unix(), true
			byEntry[relative] = failed
		}
	}
	tracked, err := loadTracking(trackingPath)
	if err != nil {
		return Report{}, err
	}
	for entry, mtime := range tracked {
		failed, exists := byEntry[entry]
		if !exists {
			failed, err = parseFailed(entry)
			if err != nil {
				return Report{}, err
			}
			failed.Path = filepath.Join(vdbRoot, filepath.FromSlash(entry))
			failed.MTimeUnix = mtime
		}
		failed.Tracked = true
		byEntry[entry] = failed
	}
	report := Report{Failed: make([]Failed, 0, len(byEntry))}
	for _, failed := range byEntry {
		report.Failed = append(report.Failed, failed)
	}
	sort.Slice(report.Failed, func(i, j int) bool { return report.Failed[i].Entry < report.Failed[j].Entry })
	return report, nil
}

func parseFailed(entry string) (Failed, error) {
	if filepath.IsAbs(entry) || filepath.ToSlash(filepath.Clean(entry)) != entry ||
		strings.Count(entry, "/") != 1 || !strings.Contains(entry, Identifier) {
		return Failed{}, fmt.Errorf("merges: invalid failed merge entry %q", entry)
	}
	cpv := strings.ReplaceAll(entry, Identifier, "")
	parsed, err := atom.Parse(cpv)
	if err != nil || parsed.Category == "" || parsed.Package == "" || parsed.Version == nil {
		return Failed{}, fmt.Errorf("merges: invalid failed merge CPV %q", cpv)
	}
	parsed.Op = atom.OpEq
	return Failed{Entry: entry, CPV: cpv, Atom: parsed.String()}, nil
}

func loadTracking(path string) (map[string]int64, error) {
	result := map[string]int64{}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("merges: open tracking file: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, fmt.Errorf("merges: malformed tracking entry %q", scanner.Text())
		}
		if _, err := parseFailed(fields[0]); err != nil {
			return nil, err
		}
		mtime, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("merges: malformed tracking timestamp %q", fields[1])
		}
		result[fields[0]] = mtime
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func SaveTracking(path string, failed []Failed) error {
	lines := make([]string, 0, len(failed))
	for _, entry := range failed {
		if _, err := parseFailed(entry.Entry); err != nil {
			return err
		}
		lines = append(lines, entry.Entry+" "+strconv.FormatInt(entry.MTimeUnix, 10))
	}
	sort.Strings(lines)
	return writeAtomic(path, []byte(strings.Join(lines, "\n")))
}

func PurgeTracking(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("merges: purge tracking: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func Cleanup(rootDir, vdbRoot, journalDir string, failed []Failed) (returnErr error) {
	cleanVDB, err := filepath.Abs(vdbRoot)
	if err != nil {
		return err
	}
	for _, entry := range failed {
		if !entry.Present {
			continue
		}
		parsed, err := parseFailed(entry.Entry)
		if err != nil {
			return err
		}
		expected := filepath.Join(cleanVDB, filepath.FromSlash(parsed.Entry))
		actual, err := filepath.Abs(entry.Path)
		if err != nil || actual != expected {
			return fmt.Errorf("merges: failed merge path %q is outside VDB entry %q", entry.Path, parsed.Entry)
		}
	}
	var operation *journal.Journal
	if filepath.Clean(rootDir) == string(filepath.Separator) {
		operation, err = journal.BeginLiveRoot(journalDir)
	} else {
		operation, err = journal.Begin(journalDir, rootDir)
	}
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			returnErr = errors.Join(returnErr, operation.Rollback())
		}
	}()
	for _, entry := range failed {
		if !entry.Present {
			continue
		}
		if err := operation.CaptureTree(entry.Path); err != nil {
			return err
		}
	}
	for _, entry := range failed {
		if entry.Present {
			if err := removeTree(operation, entry.Path); err != nil {
				return err
			}
		}
	}
	if err := operation.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func FormatTime(unix int64) string {
	return time.Unix(unix, 0).Format(time.RFC3339)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	if closeErr := directory.Close(); err == nil {
		err = closeErr
	}
	return err
}
