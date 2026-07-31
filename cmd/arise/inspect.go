package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/packageinspect"
	"github.com/airencracken/gentooling"
)

type inspectOptions struct {
	JSON         bool
	Strict       bool
	Locked       bool
	TargetKernel string
}

func runInspect(args []string) int {
	options, query, err := parseInspectOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
		return 2
	}
	paths := gentooling.DefaultSystemPaths(commandEnv("ROOT", "/"))
	paths.ConfigRoot = *portageConfigRoot
	paths.VDB = *vdbDir
	paths.World = *worldFile
	paths.ReposConf = filepath.Join(*portageConfigRoot, "repos.conf")
	paths.ActiveProfile = filepath.Join(*portageConfigRoot, "make.profile")
	if _, statErr := os.Lstat(paths.ActiveProfile); errors.Is(statErr, os.ErrNotExist) {
		paths.ActiveProfile = ""
	} else if statErr != nil {
		fmt.Fprintf(os.Stderr, "inspect: active profile: %v\n", statErr)
		return 1
	}
	integrity := gentooling.AllowPartial
	if options.Strict {
		integrity = gentooling.RequireComplete
	}
	consistency := gentooling.StabilizedLockless
	if options.Locked {
		consistency = gentooling.LockedAndStabilized
	}
	snapshot, err := gentooling.ReadSystemSnapshot(commandContext, paths, gentooling.SnapshotOptions{
		Installed:         gentooling.InstalledOptions{Integrity: integrity, IncludeContents: true},
		Config:            gentooling.ConfigOptions{Environment: os.Environ()},
		Candidates:        gentooling.CandidateOptions{Integrity: integrity},
		IncludeCandidates: true, Consistency: consistency,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect: capture system snapshot: %v\n", err)
		return 1
	}
	report, err := packageinspect.Build(commandContext, snapshot, packageinspect.Options{
		Query: query, Repositories: snapshot.Repositories, TargetKernel: options.TargetKernel,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
		if packageinspect.IsNotFound(err) {
			return 1
		}
		return 1
	}
	if options.JSON || *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "inspect: encode JSON report: %v\n", err)
			return 1
		}
		return 0
	}
	writeInspectReport(os.Stdout, report)
	return 0
}

func parseInspectOptions(args []string) (inspectOptions, string, error) {
	var options inspectOptions
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&options.JSON, "json", false, "emit a versioned JSON report")
	flags.BoolVar(&options.Strict, "strict", false, "reject incomplete package-state evidence")
	flags.BoolVar(&options.Locked, "locked", false, "observe Portage state locks in addition to stabilization")
	flags.StringVar(&options.TargetKernel, "target-kernel", "", "evaluate module rebuild state for this kernel release")
	if err := flags.Parse(args); err != nil {
		return inspectOptions{}, "", err
	}
	if flags.NArg() != 1 {
		return inspectOptions{}, "", fmt.Errorf("require exactly one package atom")
	}
	return options, flags.Arg(0), nil
}

func writeInspectReport(writer io.Writer, report packageinspect.Report) {
	fmt.Fprintf(writer, "Package inspection: %s\n", report.Query)
	fmt.Fprintf(writer, "Snapshot: %s\n", report.Consistency)
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Installed:")
	if len(report.Installed) == 0 {
		fmt.Fprintln(writer, "  none")
	}
	for _, installed := range report.Installed {
		fmt.Fprintf(writer, "  %s:%s::%s (EAPI %s)\n", installed.Package.CPV(), installed.Package.Slot, installed.Package.Repository, installed.EAPI)
		fmt.Fprintf(writer, "    USE: %s\n", strings.Join(installed.EnabledUse, " "))
		writeDependencySummary(writer, installed.DependencyAtoms, "    ")
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Available candidates:")
	if len(report.Candidates) == 0 {
		fmt.Fprintln(writer, "  none")
	}
	for _, candidate := range report.Candidates {
		status := string(candidate.Visibility.Status)
		if candidate.Visibility.Visible {
			status = "visible"
		}
		fmt.Fprintf(writer, "  %s:%s::%s (EAPI %s, %s)\n", candidate.Package.CPV(), candidate.Package.Slot, candidate.Package.Repository, candidate.EAPI, status)
		fmt.Fprintf(writer, "    KEYWORDS: %s\n", strings.Join(candidate.Keywords, " "))
		var enabled, disabled []string
		for _, decision := range candidate.Use.Decisions {
			value := decision.Name
			if decision.Forced {
				value += " (forced)"
			}
			if decision.Masked {
				value += " (masked)"
			}
			if decision.Enabled {
				enabled = append(enabled, value)
			} else {
				disabled = append(disabled, value)
			}
		}
		fmt.Fprintf(writer, "    USE enabled: %s\n", listOrNone(enabled))
		fmt.Fprintf(writer, "    USE disabled: %s\n", listOrNone(disabled))
		for _, evidence := range candidate.Visibility.Evidence {
			fmt.Fprintf(writer, "    Visibility evidence: %s %s at %s:%d\n",
				evidence.Kind, evidence.Value, evidence.Source.Path, evidence.Source.Line)
		}
		writeDependencySummary(writer, candidate.DependencyAtoms, "    ")
		requirements := candidate.KernelRequirements
		if len(requirements.Requirements) != 0 || len(requirements.Dynamic) != 0 {
			fmt.Fprintln(writer, "    Kernel requirements:")
			for _, requirement := range requirements.Requirements {
				expectation := "enabled"
				if requirement.Expectation == gentooling.KernelConfigDisabled {
					expectation = "disabled"
				}
				severity := "required"
				if requirement.Severity == gentooling.KernelRequirementWarning {
					severity = "recommended"
				}
				fmt.Fprintf(writer, "      CONFIG_%s=%s (%s)\n", requirement.Symbol, expectation, severity)
			}
			for _, dynamic := range requirements.Dynamic {
				fmt.Fprintf(writer, "      indeterminate: %s (%s)\n", dynamic.Expression, dynamic.Reason)
			}
		}
	}
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "Required by: %s\n", listOrNone(report.RequiredBy))
	fmt.Fprintln(writer, "Kernel module state:")
	if len(report.Modules) == 0 {
		fmt.Fprintln(writer, "  not an installed out-of-tree module package")
	}
	for _, module := range report.Modules {
		fmt.Fprintf(writer, "  %s: %s", module.Package.CPV(), module.Rebuild)
		if module.NeedsRebuild {
			fmt.Fprint(writer, " (rebuild required)")
		}
		fmt.Fprintln(writer)
		for _, file := range module.Modules {
			fmt.Fprintf(writer, "    %s [%s]\n", file.Path, file.KernelRelease)
		}
	}
	if len(report.Diagnostics) != 0 {
		fmt.Fprintln(writer, "Diagnostics:")
		for _, diagnostic := range report.Diagnostics {
			location := diagnostic.Path
			if location == "" {
				location = diagnostic.Package
			}
			if location == "" {
				fmt.Fprintf(writer, "  %s: %s\n", diagnostic.Code, diagnostic.Message)
			} else {
				fmt.Fprintf(writer, "  %s: %s: %s\n", diagnostic.Code, location, diagnostic.Message)
			}
		}
	}
}

func writeDependencySummary(writer io.Writer, atoms []string, indent string) {
	fmt.Fprintf(writer, "%sDependencies: %s\n", indent, listOrNone(atoms))
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, " ")
}
