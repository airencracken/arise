// Package bugreport collects a deliberately small, read-only diagnostic
// snapshot. Collection is default-deny: only fields named by Report are
// admitted and every string passes through the redactor.
package bugreport

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/klauspost/compress/zstd"
)

const SchemaVersion = 1

var writeReportFile = writeSynced

type Build struct {
	Version string `json:"version"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

type Filesystem struct {
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"free_bytes"`
	FreeInodes uint64 `json:"free_inodes"`
}

type Report struct {
	Schema      int               `json:"schema"`
	Complete    bool              `json:"complete"`
	Build       Build             `json:"build"`
	Invocation  []string          `json:"invocation"`
	Package     string            `json:"package,omitempty"`
	PlanSHA256  string            `json:"plan_sha256,omitempty"`
	ResumeAtoms []string          `json:"resume_atoms"`
	Journals    []journal.Summary `json:"journals"`
	Filesystems []Filesystem      `json:"filesystems"`
	Logs        map[string]string `json:"logs"`
	Warnings    []string          `json:"warnings"`
}

type Options struct {
	Version, Package, PlanSHA256 string
	Invocation                   []string
	ResumePath, JournalDir       string
	FilesystemPaths, LogPaths    []string
	Redactor                     *Redactor
	MaxLogBytes                  int64
}

func Collect(options Options) Report {
	redactor := options.Redactor
	if redactor == nil {
		redactor = NewRedactor()
	}
	if options.MaxLogBytes <= 0 {
		options.MaxLogBytes = 1 << 20
	}
	report := Report{
		Schema: SchemaVersion, Complete: true,
		Build:      Build{Version: redactor.String(options.Version), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH},
		Invocation: redactor.Strings(options.Invocation), Package: redactor.String(options.Package),
		ResumeAtoms: []string{}, Journals: []journal.Summary{}, Filesystems: []Filesystem{},
		Logs: map[string]string{}, Warnings: []string{},
	}
	if digest := strings.ToLower(strings.TrimSpace(options.PlanSHA256)); digest != "" {
		if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
			report.Warnings = append(report.Warnings, "plan SHA-256 was invalid and was not collected")
			report.Complete = false
		} else {
			report.PlanSHA256 = digest
		}
	}
	if options.ResumePath != "" {
		atoms, err := resolve.LoadResume(options.ResumePath)
		if err == nil {
			report.ResumeAtoms = redactor.Strings(atoms)
		} else if !os.IsNotExist(err) {
			report.Warnings = append(report.Warnings, redactor.String("resume: "+err.Error()))
			report.Complete = false
		}
	}
	if options.JournalDir != "" {
		summaries, err := journal.List(options.JournalDir)
		if err != nil {
			report.Warnings = append(report.Warnings, redactor.String("journals: "+err.Error()))
			report.Complete = false
		} else {
			for _, summary := range summaries {
				summary.Path, summary.Root = redactor.String(summary.Path), redactor.String(summary.Root)
				report.Journals = append(report.Journals, summary)
			}
		}
	}
	for _, path := range uniqueSorted(options.FilesystemPaths) {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			report.Warnings = append(report.Warnings, redactor.String(fmt.Sprintf("filesystem %s: %v", path, err)))
			report.Complete = false
			continue
		}
		report.Filesystems = append(report.Filesystems, Filesystem{
			Path: redactor.String(path), FreeBytes: stat.Bavail * uint64(stat.Bsize), FreeInodes: stat.Ffree,
		})
	}
	for _, path := range uniqueSorted(options.LogPaths) {
		data, err := readBounded(path, options.MaxLogBytes)
		if err != nil {
			report.Warnings = append(report.Warnings, redactor.String(fmt.Sprintf("log %s: %v", path, err)))
			report.Complete = false
			continue
		}
		name := redactor.String(filepath.Base(path))
		if _, exists := report.Logs[name]; exists {
			report.Warnings = append(report.Warnings, "duplicate log name "+name+" was not collected")
			report.Complete = false
			continue
		}
		report.Logs[name] = redactor.String(string(data))
	}
	sort.Strings(report.ResumeAtoms)
	sort.Strings(report.Warnings)
	return report
}

func JSON(report Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func Markdown(report Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Arise bug report\n\nSchema: %d\nComplete: %t\nVersion: %s\nPlatform: %s/%s (%s)\n",
		report.Schema, report.Complete, markdownInline(report.Build.Version), report.Build.OS, report.Build.Arch, report.Build.Go)
	if report.Package != "" {
		fmt.Fprintf(&out, "Package: `%s`\n", markdownInline(report.Package))
	}
	if report.PlanSHA256 != "" {
		fmt.Fprintf(&out, "Plan SHA-256: `%s`\n", report.PlanSHA256)
	}
	fmt.Fprintf(&out, "\n## Invocation\n\n`%s`\n\n", markdownInline(strings.Join(report.Invocation, " ")))
	fmt.Fprintf(&out, "## Recovery state\n\nResume entries: %d\nJournals: %d\n\n", len(report.ResumeAtoms), len(report.Journals))
	fmt.Fprintf(&out, "## Filesystems\n\n")
	for _, fs := range report.Filesystems {
		fmt.Fprintf(&out, "- `%s`: %d bytes and %d inodes free\n", fs.Path, fs.FreeBytes, fs.FreeInodes)
	}
	fmt.Fprintf(&out, "\n## Logs\n\n")
	names := make([]string, 0, len(report.Logs))
	for name := range report.Logs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&out, "### %s\n\n%s\n\n", markdownInline(name), indentMarkdown(report.Logs[name]))
	}
	if len(report.Warnings) != 0 {
		fmt.Fprintf(&out, "## Collection warnings\n\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&out, "- %s\n", warning)
		}
	}
	return out.String()
}

func markdownInline(value string) string {
	return strings.NewReplacer("\\", "\\\\", "`", "\\`", "\r", " ", "\n", " ").Replace(value)
}

func indentMarkdown(value string) string {
	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	for index := range lines {
		lines[index] = "    " + lines[index]
	}
	return strings.Join(lines, "\n")
}

func WriteDirectory(path string, report Report) error {
	if path == "" || filepath.Clean(path) == "." || filepath.Clean(path) == string(filepath.Separator) {
		return fmt.Errorf("bug report: unsafe output directory %q", path)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("bug report: output already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bug report: inspect output: %w", err)
	}
	parent := filepath.Dir(path)
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("bug report: create staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return fmt.Errorf("bug report: secure staging directory: %w", err)
	}
	jsonData, err := JSON(report)
	if err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{name: "report.json", data: append(jsonData, '\n')},
		{name: "report.md", data: []byte(Markdown(report))},
	}
	for _, file := range files {
		if err := writeReportFile(filepath.Join(staging, file.name), file.data); err != nil {
			return fmt.Errorf("bug report: write %s: %w", file.name, err)
		}
	}
	if err := syncDirectory(staging); err != nil {
		return fmt.Errorf("bug report: sync staging directory: %w", err)
	}
	if err := os.Rename(staging, path); err != nil {
		return fmt.Errorf("bug report: publish output: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("bug report: sync output parent: %w", err)
	}
	return nil
}

func writeSynced(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
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

func WriteArchive(w io.Writer, report Report) error {
	jsonData, err := JSON(report)
	if err != nil {
		return err
	}
	files := map[string][]byte{"report.json": append(jsonData, '\n'), "report.md": []byte(Markdown(report))}
	encoder, err := zstd.NewWriter(w, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(encoder)
	for _, name := range []string{"report.json", "report.md"} {
		data := files[name]
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0), Uid: 0, Gid: 0}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return encoder.Close()
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var buffer bytes.Buffer
	n, err := io.CopyN(&buffer, file, limit+1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n > limit {
		return nil, fmt.Errorf("exceeds %d byte collection limit", limit)
	}
	return buffer.Bytes(), nil
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
