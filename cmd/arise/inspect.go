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

	"github.com/airencracken/arise/internal/color"
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
		if packageinspect.IsNotFound(err) && len(report.Diagnostics) != 0 {
			if options.JSON || *jsonOutput {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				if encodeErr := encoder.Encode(report); encodeErr != nil {
					fmt.Fprintf(os.Stderr, "inspect: encode partial JSON report: %v\n", encodeErr)
					return 1
				}
			} else {
				writeInspectReport(os.Stdout, report)
			}
		}
		fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
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
	fmt.Fprintf(writer, "%s %s\n", color.BoldCyan("Package inspection:"), color.Bold(report.Query))
	fmt.Fprintf(writer, "%s %s\n", color.Bold("Snapshot:"), color.Cyan(report.Consistency))
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, color.BoldCyan("Installed:"))
	if len(report.Installed) == 0 {
		fmt.Fprintln(writer, "  none")
	}
	for _, installed := range report.Installed {
		identity := fmt.Sprintf("%s:%s::%s", installed.Package.CPV(), installed.Package.Slot, installed.Package.Repository)
		fmt.Fprintf(writer, "  %s (EAPI %s)\n", color.InstalledVersion(identity), installed.EAPI)
		fmt.Fprintf(writer, "    %s %s\n", color.Bold("USE:"), color.Green(strings.Join(installed.EnabledUse, " ")))
		writeDependencySummary(writer, installed.DependencyAtoms, "    ")
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, color.BoldCyan("Available candidates:"))
	if len(report.Candidates) == 0 {
		fmt.Fprintln(writer, "  none")
	}
	for _, candidate := range report.Candidates {
		status := string(candidate.Visibility.Status)
		if candidate.Visibility.Visible {
			status = "visible"
		}
		identity := fmt.Sprintf("%s:%s::%s", candidate.Package.CPV(), candidate.Package.Slot, candidate.Package.Repository)
		statusDisplay := color.Green(status)
		if !candidate.Visibility.Visible {
			statusDisplay = color.Yellow(status)
		}
		fmt.Fprintf(writer, "  %s (EAPI %s, %s)\n", color.Bold(identity), candidate.EAPI, statusDisplay)
		fmt.Fprintf(writer, "    %s %s\n", color.Bold("KEYWORDS:"), color.Cyan(strings.Join(candidate.Keywords, " ")))
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
		fmt.Fprintf(writer, "    %s %s\n", color.Bold("USE enabled:"), color.Green(listOrNone(enabled)))
		fmt.Fprintf(writer, "    %s %s\n", color.Bold("USE disabled:"), color.Red(listOrNone(disabled)))
		for _, evidence := range candidate.Visibility.Evidence {
			fmt.Fprintf(writer, "    %s %s %s at %s:%d\n", color.Bold("Visibility evidence:"),
				evidence.Kind, evidence.Value, evidence.Source.Path, evidence.Source.Line)
		}
		writeDependencySummary(writer, candidate.DependencyAtoms, "    ")
		requirements := candidate.KernelRequirements
		if len(requirements.Requirements) != 0 || len(requirements.Unresolved) != 0 {
			fmt.Fprintln(writer, color.Bold("    Kernel requirements:"))
			for _, requirement := range requirements.Requirements {
				if requirement.Applicability == gentooling.Inapplicable {
					continue
				}
				expectation := "enabled"
				if requirement.Expectation == gentooling.KernelConfigDisabled {
					expectation = "disabled"
				}
				severity := "required"
				if requirement.Severity == gentooling.KernelRequirementWarning {
					severity = "recommended"
				}
				fmt.Fprintf(writer, "      %s=%s (%s, %s)\n", color.Cyan("CONFIG_"+requirement.Symbol), expectation, severity, requirement.Applicability)
			}
			for _, unresolved := range requirements.Unresolved {
				if unresolved.Applicability == gentooling.Inapplicable {
					continue
				}
				fmt.Fprintf(writer, "      %s %s (%s)\n", color.Yellow("indeterminate:"), unresolved.DeveloperText, unresolved.OperatorText)
			}
		}
	}
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "%s %s\n", color.BoldCyan("Required by:"), listOrNone(report.RequiredBy))
	fmt.Fprintln(writer, color.BoldCyan("Kernel module state:"))
	if len(report.Modules) == 0 {
		fmt.Fprintln(writer, "  not an installed out-of-tree module package")
	}
	for _, module := range report.Modules {
		fmt.Fprintf(writer, "  %s: %s", color.Bold(module.Package.CPV()), module.Rebuild)
		if module.NeedsRebuild {
			fmt.Fprint(writer, " (rebuild required)")
		}
		fmt.Fprintln(writer)
		for _, file := range module.Modules {
			fmt.Fprintf(writer, "    %s [%s]\n", file.Path, file.KernelRelease)
		}
	}
	if len(report.Diagnostics) != 0 {
		fmt.Fprintln(writer, color.BoldYellow("Diagnostics:"))
		for _, diagnostic := range report.Diagnostics {
			location := diagnostic.Path
			if location == "" {
				location = diagnostic.Package
			}
			if location == "" {
				fmt.Fprintf(writer, "  %s: %s\n", color.Yellow(diagnostic.Code), diagnostic.Message)
			} else {
				fmt.Fprintf(writer, "  %s: %s: %s\n", color.Yellow(diagnostic.Code), location, diagnostic.Message)
			}
		}
	}
}

func writeDependencySummary(writer io.Writer, atoms []string, indent string) {
	fmt.Fprintf(writer, "%s%s %s\n", indent, color.Bold("Dependencies:"), listOrNone(atoms))
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, " ")
}
