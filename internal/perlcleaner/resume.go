package perlcleaner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ResumeState struct {
	Schema          int      `json:"schema"`
	Operation       string   `json:"operation"`
	Stage           string   `json:"stage"`
	PlanSHA256      string   `json:"plan_sha256"`
	Mode            Mode     `json:"mode"`
	DeleteLeftovers bool     `json:"delete_leftovers"`
	ABI             ABI      `json:"abi"`
	Targets         []string `json:"targets"`
}

func NewResumeState(report Report, targets []string, deleteLeftovers bool) ResumeState {
	state := ResumeState{
		Schema: 1, Operation: "perl-cleaner", Stage: "rebuild",
		Mode: report.Mode, DeleteLeftovers: deleteLeftovers, ABI: report.ABI,
		Targets: append([]string(nil), targets...),
	}
	state.PlanSHA256 = resumeDigest(state)
	return state
}

func SaveResume(path string, state ResumeState) error {
	if err := validateResume(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeResumeAtomic(path, data)
}

func LoadResume(path string) (ResumeState, error) {
	file, err := os.Open(path)
	if err != nil {
		return ResumeState{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	var state ResumeState
	if err := decoder.Decode(&state); err != nil {
		return ResumeState{}, fmt.Errorf("perl-cleaner: decode resume state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return ResumeState{}, fmt.Errorf("perl-cleaner: decode resume state: %w", err)
	}
	if err := validateResume(state); err != nil {
		return ResumeState{}, err
	}
	return state, nil
}

func RemoveResume(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncResumeDirectory(filepath.Dir(path))
}

func validateResume(state ResumeState) error {
	if state.Schema != 1 || state.Operation != "perl-cleaner" || state.Stage != "rebuild" {
		return fmt.Errorf("perl-cleaner: invalid resume contract")
	}
	if !state.Mode.Modules && !state.Mode.LibPerl {
		return fmt.Errorf("perl-cleaner: resume state has no repair mode")
	}
	if state.ABI.Version == "" || state.ABI.SourceCPV == "" || len(state.ABI.LibPerlSONames) == 0 {
		return fmt.Errorf("perl-cleaner: resume state has incomplete ABI evidence")
	}
	if len(state.Targets) == 0 {
		return fmt.Errorf("perl-cleaner: resume state has no targets")
	}
	for _, target := range state.Targets {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("perl-cleaner: resume state has empty target")
		}
	}
	if len(state.PlanSHA256) != 64 || state.PlanSHA256 != resumeDigest(state) {
		return fmt.Errorf("perl-cleaner: resume state integrity check failed")
	}
	return nil
}

func resumeDigest(state ResumeState) string {
	state.PlanSHA256 = ""
	data, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type resumeFile interface {
	io.Writer
	Sync() error
	Chmod(os.FileMode) error
	Close() error
}

var (
	createResumeTemp = func(directory, pattern string) (resumeFile, error) {
		return os.CreateTemp(directory, pattern)
	}
	renameResume        = os.Rename
	syncResumeDirectory = func(path string) error {
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
)

func writeResumeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := createResumeTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := ""
	if named, ok := file.(interface{ Name() string }); ok {
		temporary = named.Name()
	}
	if temporary == "" {
		_ = file.Close()
		return fmt.Errorf("perl-cleaner: temporary resume file has no name")
	}
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
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
	if err := renameResume(temporary, path); err != nil {
		return err
	}
	return syncResumeDirectory(filepath.Dir(path))
}
