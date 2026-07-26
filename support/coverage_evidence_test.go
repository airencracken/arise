package support

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type coverageEvidence struct {
	Schema        int    `json:"schema"`
	DocumentClass string `json:"document_class"`
	Date          string `json:"date"`
	Commit        string `json:"commit"`
	GoVersion     string `json:"go_version"`
	Lanes         struct {
		Core struct {
			StatementPercent       float64  `json:"statement_percent"`
			PreviousPercent        *float64 `json:"previous_statement_percent"`
			ChangePercentagePoints *float64 `json:"change_percentage_points"`
			TimeoutSeconds         int      `json:"timeout_seconds"`
			TestExecutionExcludes  []string `json:"test_execution_excludes"`
			Instrumentation        string   `json:"instrumentation"`
			Status                 string   `json:"status"`
		} `json:"core"`
		Network   json.RawMessage `json:"network"`
		Benchmark json.RawMessage `json:"benchmark"`
	} `json:"lanes"`
	PackageLocal map[string]float64 `json:"package_local_statement_percent"`
}

func TestCoverageEvidenceSchema(t *testing.T) {
	root := repositoryRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "docs", "evidence", "COVERAGE_BASELINE_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no coverage baselines found")
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var evidence coverageEvidence
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&evidence); err != nil {
				t.Fatalf("invalid coverage evidence: %v", err)
			}
			validateCoverageEvidence(t, evidence)
		})
	}
}

func TestCoverageEvidenceRejectsAdversarialValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*coverageEvidence)
	}{
		{"negative coverage", func(e *coverageEvidence) { e.Lanes.Core.StatementPercent = -0.1 }},
		{"coverage over one hundred", func(e *coverageEvidence) { e.Lanes.Core.StatementPercent = 100.1 }},
		{"path traversal package", func(e *coverageEvidence) { e.PackageLocal["../outside"] = 50 }},
		{"absolute package", func(e *coverageEvidence) { e.PackageLocal["/tmp/outside"] = 50 }},
		{"invalid status", func(e *coverageEvidence) { e.Lanes.Core.Status = "maybe" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validCoverageEvidence()
			test.mutate(&evidence)
			if coverageEvidenceValid(evidence) {
				t.Fatal("adversarial evidence was accepted")
			}
		})
	}
}

func TestCoverageEvidenceChangeIsArithmeticallyConsistent(t *testing.T) {
	evidence := validCoverageEvidence()
	previous := 68.4
	change := 1.7
	evidence.Lanes.Core.StatementPercent = 70.1
	evidence.Lanes.Core.PreviousPercent = &previous
	evidence.Lanes.Core.ChangePercentagePoints = &change
	if !coverageEvidenceValid(evidence) {
		t.Fatal("valid coverage delta was rejected")
	}
	change = 1.6
	if coverageEvidenceValid(evidence) {
		t.Fatal("inconsistent coverage delta was accepted")
	}
}

func validateCoverageEvidence(t *testing.T, evidence coverageEvidence) {
	t.Helper()
	if !coverageEvidenceValid(evidence) {
		t.Fatalf("coverage evidence failed contract validation: %+v", evidence)
	}
}

func coverageEvidenceValid(evidence coverageEvidence) bool {
	if evidence.Schema < 1 || evidence.Schema > 2 ||
		evidence.DocumentClass != "development-evidence" ||
		evidence.Lanes.Core.Instrumentation == "" ||
		evidence.Lanes.Core.TimeoutSeconds <= 0 ||
		!validPercent(evidence.Lanes.Core.StatementPercent) {
		return false
	}
	if _, err := time.Parse("2006-01-02", evidence.Date); err != nil {
		return false
	}
	switch evidence.Lanes.Core.Status {
	case "pass", "blocked_in_workspace_sandbox":
	default:
		return false
	}
	if evidence.Schema >= 2 {
		if len(evidence.Commit) != 40 || evidence.GoVersion == "" || len(evidence.PackageLocal) == 0 {
			return false
		}
		for name, percent := range evidence.PackageLocal {
			if name == "" || filepath.IsAbs(name) || strings.Contains(name, "..") || !validPercent(percent) {
				return false
			}
		}
		if (evidence.Lanes.Core.PreviousPercent == nil) != (evidence.Lanes.Core.ChangePercentagePoints == nil) {
			return false
		}
		if evidence.Lanes.Core.PreviousPercent != nil {
			want := evidence.Lanes.Core.StatementPercent - *evidence.Lanes.Core.PreviousPercent
			if difference(want, *evidence.Lanes.Core.ChangePercentagePoints) > 0.0001 {
				return false
			}
		}
	}
	return true
}

func validPercent(value float64) bool {
	return value >= 0 && value <= 100
}

func difference(a, b float64) float64 {
	if a < b {
		return b - a
	}
	return a - b
}

func validCoverageEvidence() coverageEvidence {
	previous := 68.4
	change := 2.2
	var evidence coverageEvidence
	evidence.Schema = 2
	evidence.DocumentClass = "development-evidence"
	evidence.Date = "2026-07-25"
	evidence.Commit = strings.Repeat("a", 40)
	evidence.GoVersion = "1.26.5"
	evidence.Lanes.Core.StatementPercent = 70.6
	evidence.Lanes.Core.PreviousPercent = &previous
	evidence.Lanes.Core.ChangePercentagePoints = &change
	evidence.Lanes.Core.TimeoutSeconds = 60
	evidence.Lanes.Core.Instrumentation = "./..."
	evidence.Lanes.Core.Status = "pass"
	evidence.PackageLocal = map[string]float64{"internal/example": 50}
	return evidence
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate coverage evidence test")
	}
	return filepath.Dir(filepath.Dir(filename))
}
