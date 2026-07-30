// Package resumemaint audits and clears Arise and Portage resume records.
package resumemaint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/resolve"
)

type Record struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Present   bool   `json:"present"`
	Valid     bool   `json:"valid"`
	Remaining int    `json:"remaining,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Report struct {
	Arise       Record   `json:"arise"`
	Portage     Record   `json:"portage"`
	PortageKeys []string `json:"portage_keys,omitempty"`
}

var (
	removePath     = os.Remove
	replaceMTimeDB = writeMTimeDB
)

func Check(arisePath, mtimedbPath string) (Report, error) {
	report := Report{
		Arise:   Record{Name: "arise", Path: arisePath, Valid: true},
		Portage: Record{Name: "portage", Path: mtimedbPath, Valid: true},
	}
	if _, err := os.Stat(arisePath); err == nil {
		report.Arise.Present = true
		atoms, loadErr := resolve.LoadResume(arisePath)
		if loadErr != nil {
			report.Arise.Valid = false
			report.Arise.Error = loadErr.Error()
		} else {
			report.Arise.Remaining = len(atoms)
		}
	} else if !os.IsNotExist(err) {
		return Report{}, fmt.Errorf("resume: inspect Arise state: %w", err)
	}
	records, present, err := readMTimeDB(mtimedbPath)
	if err != nil {
		report.Portage.Present = true
		report.Portage.Valid = false
		report.Portage.Error = err.Error()
		return report, nil
	}
	if present {
		for _, key := range []string{"resume", "resume_backup"} {
			if _, exists := records[key]; exists {
				report.PortageKeys = append(report.PortageKeys, key)
			}
		}
		report.Portage.Present = len(report.PortageKeys) != 0
		report.Portage.Remaining = len(report.PortageKeys)
	}
	return report, nil
}

func (report Report) HasState() bool {
	return report.Arise.Present || report.Portage.Present
}

func (report Report) Valid() bool {
	return report.Arise.Valid && report.Portage.Valid
}

func Clean(rootDir, journalDir string, report Report) (returnErr error) {
	if !report.Valid() {
		return fmt.Errorf("resume: refusing to clean invalid state")
	}
	records, _, err := readMTimeDB(report.Portage.Path)
	if err != nil {
		return err
	}
	for _, key := range []string{"resume", "resume_backup"} {
		delete(records, key)
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
	if err := operation.CaptureBatch([]string{report.Arise.Path, report.Portage.Path}); err != nil {
		return err
	}
	if report.Arise.Present {
		if err := removePath(report.Arise.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("resume: remove Arise state: %w", err)
		}
	}
	if report.Portage.Present {
		if err := replaceMTimeDB(report.Portage.Path, records); err != nil {
			return fmt.Errorf("resume: update Portage state: %w", err)
		}
	}
	if err := operation.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func readMTimeDB(path string) (map[string]json.RawMessage, bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("resume: open Portage mtimedb: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var records map[string]json.RawMessage
	if err := decoder.Decode(&records); err != nil {
		return nil, true, fmt.Errorf("resume: parse Portage mtimedb: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, true, fmt.Errorf("resume: parse Portage mtimedb: %w", err)
	}
	if records == nil {
		return nil, true, fmt.Errorf("resume: Portage mtimedb must be a JSON object")
	}
	return records, true, nil
}

func writeMTimeDB(path string, records map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o644); err == nil {
		_, err = io.Copy(file, bytes.NewReader(data))
	}
	if err == nil {
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
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = directory.Sync()
	if closeErr := directory.Close(); err == nil {
		err = closeErr
	}
	return err
}
