package pythoncleaner

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

const (
	ResumeStageCohort     = "repair-cohort"
	ResumeStageValidate   = "validate-runtime"
	ResumeStagePreference = "switch-preference"
	ResumeStageComplete   = "complete"
)

type ResumeState struct {
	Schema           int        `json:"schema"`
	Operation        string     `json:"operation"`
	Stage            string     `json:"stage"`
	PlanSHA256       string     `json:"plan_sha256"`
	Policy           Policy     `json:"policy"`
	CurrentTargets   []string   `json:"current_targets"`
	CompletedCohorts [][]string `json:"completed_cohorts"`
}

func NewResumeState(policy Policy, stage string, current []string, completed [][]string) ResumeState {
	state := ResumeState{
		Schema: 1, Operation: "python-cleaner", Stage: stage, Policy: policy,
		CurrentTargets:   append([]string(nil), current...),
		CompletedCohorts: cloneCohorts(completed),
	}
	state.PlanSHA256 = pythonResumeDigest(state)
	return state
}

func AdvanceResume(state ResumeState, stage string, current []string) ResumeState {
	if state.Stage == ResumeStageCohort && len(state.CurrentTargets) != 0 {
		state.CompletedCohorts = append(state.CompletedCohorts, append([]string(nil), state.CurrentTargets...))
	}
	state.Stage = stage
	state.CurrentTargets = append([]string(nil), current...)
	state.PlanSHA256 = pythonResumeDigest(state)
	return state
}

func SaveResume(path string, state ResumeState) error {
	if err := validatePythonResume(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writePythonResumeAtomic(path, data)
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
		return ResumeState{}, fmt.Errorf("python-cleaner: decode resume state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return ResumeState{}, fmt.Errorf("python-cleaner: decode resume state: %w", err)
	}
	if err := validatePythonResume(state); err != nil {
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
	return syncPythonResumeDirectory(filepath.Dir(path))
}

func validatePythonResume(state ResumeState) error {
	if state.Schema != 1 || state.Operation != "python-cleaner" {
		return fmt.Errorf("python-cleaner: invalid resume contract")
	}
	switch state.Stage {
	case ResumeStageCohort:
		if len(state.CurrentTargets) == 0 {
			return fmt.Errorf("python-cleaner: cohort resume state has no targets")
		}
	case ResumeStageValidate, ResumeStagePreference, ResumeStageComplete:
		if len(state.CurrentTargets) != 0 {
			return fmt.Errorf("python-cleaner: non-cohort resume state has package targets")
		}
	default:
		return fmt.Errorf("python-cleaner: invalid resume stage %q", state.Stage)
	}
	if len(state.Policy.Targets) == 0 {
		return fmt.Errorf("python-cleaner: resume state has no policy targets")
	}
	for _, target := range state.Policy.Targets {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("python-cleaner: resume state has empty policy target")
		}
	}
	cohorts := cloneCohorts(state.CompletedCohorts)
	if len(state.CurrentTargets) != 0 {
		cohorts = append(cohorts, state.CurrentTargets)
	}
	for _, cohort := range cohorts {
		if len(cohort) == 0 {
			return fmt.Errorf("python-cleaner: resume state has empty cohort")
		}
		for _, target := range cohort {
			if strings.TrimSpace(target) == "" {
				return fmt.Errorf("python-cleaner: resume state has empty package target")
			}
		}
	}
	if len(state.PlanSHA256) != 64 || state.PlanSHA256 != pythonResumeDigest(state) {
		return fmt.Errorf("python-cleaner: resume state integrity check failed")
	}
	return nil
}

func pythonResumeDigest(state ResumeState) string {
	state.PlanSHA256 = ""
	data, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func cloneCohorts(cohorts [][]string) [][]string {
	result := make([][]string, len(cohorts))
	for index := range cohorts {
		result[index] = append([]string(nil), cohorts[index]...)
	}
	return result
}

type pythonResumeFile interface {
	io.Writer
	Sync() error
	Chmod(os.FileMode) error
	Close() error
}

var (
	createPythonResumeTemp = func(directory, pattern string) (pythonResumeFile, error) {
		return os.CreateTemp(directory, pattern)
	}
	renamePythonResume        = os.Rename
	syncPythonResumeDirectory = func(path string) error {
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

func writePythonResumeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := createPythonResumeTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := ""
	if named, ok := file.(interface{ Name() string }); ok {
		temporary = named.Name()
	}
	if temporary == "" {
		_ = file.Close()
		return fmt.Errorf("python-cleaner: temporary resume file has no name")
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
	if err := renamePythonResume(temporary, path); err != nil {
		return err
	}
	return syncPythonResumeDirectory(filepath.Dir(path))
}
