package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/configdoctor"
)

func TestWriteDoctorReportHumanAndJSONContracts(t *testing.T) {
	report := configdoctor.Report{Schema: 1, Findings: []configdoctor.Finding{{Kind: "stale-atom", Severity: "warning", Family: "package.use", Atom: "cat/pkg", Rule: 2, Message: "unused"}}}
	var human bytes.Buffer
	if err := writeDoctorReport(&human, report, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"warning", "stale-atom", "rule 2", "cat/pkg"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human report %q lacks %q", human.String(), want)
		}
	}
	var machine bytes.Buffer
	if err := writeDoctorReport(&machine, report, true); err != nil {
		t.Fatal(err)
	}
	var decoded configdoctor.Report
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != configdoctor.SchemaVersion || len(decoded.Findings) != 1 {
		t.Fatalf("JSON report = %#v", decoded)
	}
}

func TestWriteDoctorReportEmptyStateIsExplicit(t *testing.T) {
	var output bytes.Buffer
	if err := writeDoctorReport(&output, configdoctor.Report{Schema: 1, Findings: []configdoctor.Finding{}}, false); err != nil {
		t.Fatal(err)
	}
	if output.String() != "No configuration findings.\n" {
		t.Fatalf("empty report = %q", output.String())
	}
}
