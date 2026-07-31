package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/packageinspect"
	"github.com/airencracken/gentooling"
)

func TestParseInspectOptions(t *testing.T) {
	options, query, err := parseInspectOptions([]string{"--json", "--strict", "--locked", "--target-kernel", "6.13", "sys-fs/zfs"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.JSON || !options.Strict || !options.Locked || options.TargetKernel != "6.13" || query != "sys-fs/zfs" {
		t.Fatalf("options = %+v, query = %q", options, query)
	}
}

func TestParseInspectOptionsRequiresExactlyOneAtom(t *testing.T) {
	for _, args := range [][]string{nil, {"one", "two"}, {"--unknown", "one"}} {
		if _, _, err := parseInspectOptions(args); err == nil {
			t.Fatalf("parseInspectOptions(%v) succeeded", args)
		}
	}
}

func TestWriteInspectReportHumanContract(t *testing.T) {
	report := packageinspect.Report{
		Schema: packageinspect.Schema, Query: "sys-fs/zfs", Consistency: "stabilized-lockless",
		Installed: []packageinspect.Installed{{Package: gentooling.PackageID{
			Category: "sys-fs", Name: "zfs", Version: "2.3", Slot: "0", Repository: "gentoo",
		}, EAPI: "8", EnabledUse: []string{"kernel-builtin"}}},
		Candidates: []packageinspect.Candidate{{Package: gentooling.PackageID{
			Category: "sys-fs", Name: "zfs", Version: "2.3", Slot: "0", Repository: "gentoo",
		}, EAPI: "8", Keywords: []string{"amd64"}, Visibility: gentooling.VisibilityResult{
			Visible: true, Status: gentooling.VisibilityVisible,
		}, Use: gentooling.UseEvaluation{Decisions: []gentooling.UseDecision{{Name: "kernel-builtin", Enabled: true}}},
			KernelRequirements: gentooling.KernelRequirementSet{Requirements: []gentooling.KernelConfigRequirement{{
				Symbol: "ZFS", Expectation: gentooling.KernelConfigDisabled, Severity: gentooling.KernelRequirementWarning,
			}}},
		}},
		RequiredBy: []string{}, Modules: []gentooling.InstalledKernelModulePackage{}, Diagnostics: []packageinspect.Diagnostic{},
	}
	var output bytes.Buffer
	writeInspectReport(&output, report)
	for _, expected := range []string{
		"Package inspection: sys-fs/zfs", "Snapshot: stabilized-lockless",
		"sys-fs/zfs-2.3:0::gentoo", "USE enabled: kernel-builtin",
		"CONFIG_ZFS=disabled (recommended)", "Required by: none",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output missing %q:\n%s", expected, output.String())
		}
	}
}
