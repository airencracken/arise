package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/portage"
)

func runProfile(args []string) int {
	return runProfileWithWriters(args, os.Stdout, os.Stderr)
}

func runProfileWithWriters(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" {
		fmt.Fprintln(stderr, "profile: usage: arise profile show [--explain] [--json]")
		return 2
	}
	flags := flag.NewFlagSet("profile show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	explain := flags.Bool("explain", *explainOutput, "show ordered contributions with file and line")
	jsonMode := flags.Bool("json", *jsonOutput, "emit the arise.profile.v1 report with provenance")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: arise profile show [--explain] [--json]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "profile show: unexpected operands")
		return 2
	}
	report, err := portage.InspectProfile(commandContext, *portageConfigRoot)
	if err != nil {
		fmt.Fprintf(stderr, "profile show: %v\n", err)
		return 1
	}
	var output bytes.Buffer
	if *jsonMode {
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "profile show: encode: %v\n", err)
			return 1
		}
	} else {
		writeProfileReport(&output, report, *explain)
	}
	if _, err := io.Copy(stdout, &output); err != nil {
		fmt.Fprintf(stderr, "profile show: write: %v\n", err)
		return 1
	}
	return 0
}

func writeProfileReport(writer io.Writer, report portage.ProfileReport, explain bool) {
	fmt.Fprintf(writer, "Profile: %s\nScope: profile defaults and policy\n", report.ActivePath)
	fmt.Fprintln(writer, "Local configuration, make.globals, command environment and package IUSE are excluded.")
	fmt.Fprintln(writer, "\nInheritance (evaluation order):")
	for index, layer := range report.Layers {
		fmt.Fprintf(writer, "  %d. %s\n", index+1, layer.Path)
		if explain && len(layer.Parents) != 0 {
			fmt.Fprintf(writer, "     parent references: %s\n", strings.Join(layer.Parents, ", "))
		}
	}
	fmt.Fprintln(writer, "\nDefaults (negative USE tokens retain explicit disable requests):")
	for _, value := range report.Defaults {
		fmt.Fprintf(writer, "  %s=%q\n", value.Name, value.Value)
		if explain {
			writeProfileContributions(writer, value.Contributions)
		}
	}
	for _, policy := range report.Policies {
		label := "effective entries"
		if policy.Kind == "ordered_rules" {
			label = "ordered rules; evaluated per package"
		}
		fmt.Fprintf(writer, "\n%s (%s):\n", policy.Name, label)
		if len(policy.Values) == 0 {
			fmt.Fprintln(writer, "  (none)")
		}
		for _, value := range policy.Values {
			fmt.Fprintf(writer, "  %s\n", value)
		}
		if explain {
			writeProfileContributions(writer, policy.Contributions)
		}
	}
	if len(report.Unsupported) != 0 {
		fmt.Fprintln(writer, "\nFiles outside this report's supported policy (not evaluated):")
		for _, path := range report.Unsupported {
			fmt.Fprintf(writer, "  %s\n", path)
		}
	}
}

func writeProfileContributions(writer io.Writer, changes []portage.ProfileContribution) {
	for _, change := range changes {
		fmt.Fprintf(writer, "    %s:%d: %q\n", change.Source.Path, change.Source.Line, change.Value)
	}
}
