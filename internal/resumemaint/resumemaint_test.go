package resumemaint

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFindsBothResumeFormats(t *testing.T) {
	root := t.TempDir()
	arise := filepath.Join(root, "var/tmp/arise/resume")
	mtimedb := filepath.Join(root, "var/cache/edb/mtimedb")
	writeFile(t, arise, `{"packages":[{"cpv":"cat/pkg-1","atom":"=cat/pkg-1","completed":false},{"cpv":"cat/done-1","atom":"=cat/done-1","completed":true}]}`)
	writeFile(t, mtimedb, `{"resume":{"mergelist":[1]},"resume_backup":{},"updates":{"keep":true}}`)

	report, err := Check(arise, mtimedb)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() || !report.HasState() || report.Arise.Remaining != 1 {
		t.Fatalf("report = %#v", report)
	}
	if got := strings.Join(report.PortageKeys, ","); got != "resume,resume_backup" {
		t.Fatalf("Portage keys = %q", got)
	}
}

func TestCheckReportsMalformedInputsWithoutLosingTheirPresence(t *testing.T) {
	cases := []struct {
		name, arise, mtimedb string
	}{
		{"arise", `{"packages":null}`, `{}`},
		{"portage", `{"packages":[]}`, `[]`},
		{"trailing", `{"packages":[]}`, `{} {}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			arise := filepath.Join(root, "resume")
			mtimedb := filepath.Join(root, "mtimedb")
			writeFile(t, arise, test.arise)
			writeFile(t, mtimedb, test.mtimedb)
			report, err := Check(arise, mtimedb)
			if err != nil {
				t.Fatal(err)
			}
			if report.Valid() {
				t.Fatalf("malformed state reported valid: %#v", report)
			}
		})
	}
}

func TestCleanPreservesUnrelatedMTimeDBKeysAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	arise := filepath.Join(root, "var/tmp/arise/resume")
	mtimedb := filepath.Join(root, "var/cache/edb/mtimedb")
	writeFile(t, arise, `{"packages":[]}`)
	writeFile(t, mtimedb, `{"resume":{"drop":1},"resume_backup":[2],"updates":{"nested":[1,2.5,9007199254740993]},"version":"keep"}`)
	report, err := Check(arise, mtimedb)
	if err != nil {
		t.Fatal(err)
	}
	if err := Clean(root, filepath.Join(root, "journals"), report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(arise); !os.IsNotExist(err) {
		t.Fatalf("Arise resume remains: %v", err)
	}
	var records map[string]json.RawMessage
	data, err := os.ReadFile(mtimedb)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if _, exists := records["resume"]; exists {
		t.Fatal("resume key remains")
	}
	if _, exists := records["resume_backup"]; exists {
		t.Fatal("resume_backup key remains")
	}
	var compactUpdates bytes.Buffer
	if err := json.Compact(&compactUpdates, records["updates"]); err != nil {
		t.Fatal(err)
	}
	if compactUpdates.String() != `{"nested":[1,2.5,9007199254740993]}` || string(records["version"]) != `"keep"` {
		t.Fatalf("unrelated values changed: %s", data)
	}
	clean, err := Check(arise, mtimedb)
	if err != nil {
		t.Fatal(err)
	}
	if clean.HasState() {
		t.Fatalf("state remains: %#v", clean)
	}
	if err := Clean(root, filepath.Join(root, "journals-2"), clean); err != nil {
		t.Fatalf("idempotent clean: %v", err)
	}
}

func TestCleanRollsBackBothFilesOnSecondMutationFailure(t *testing.T) {
	root := t.TempDir()
	arise := filepath.Join(root, "resume")
	mtimedb := filepath.Join(root, "mtimedb")
	ariseData := `{"packages":[]}`
	mtimedbData := `{"resume":{},"keep":{"x":1}}`
	writeFile(t, arise, ariseData)
	writeFile(t, mtimedb, mtimedbData)
	report, err := Check(arise, mtimedb)
	if err != nil {
		t.Fatal(err)
	}
	oldReplace := replaceMTimeDB
	t.Cleanup(func() { replaceMTimeDB = oldReplace })
	replaceMTimeDB = func(string, map[string]json.RawMessage) error {
		return errors.New("injected mtimedb failure")
	}
	err = Clean(root, filepath.Join(root, "journals"), report)
	if err == nil || !strings.Contains(err.Error(), "injected mtimedb failure") {
		t.Fatalf("Clean error = %v", err)
	}
	for path, want := range map[string]string{arise: ariseData, mtimedb: mtimedbData} {
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != want {
			t.Fatalf("rollback %s = %q, %v; want %q", path, got, readErr, want)
		}
	}
}

func TestCleanRefusesInvalidState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "resume")
	writeFile(t, path, "not json")
	report, err := Check(path, filepath.Join(root, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Clean(root, filepath.Join(root, "journals"), report); err == nil {
		t.Fatal("Clean accepted invalid state")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "not json" {
		t.Fatalf("invalid state changed: %q, %v", data, err)
	}
}
