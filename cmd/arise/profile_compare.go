package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
)

func runProfileList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("profile list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonMode := flags.Bool("json", *jsonOutput, "emit arise.profile-catalog.v1")
	all := flags.Bool("all", false, "include all advertised architectures")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: arise profile list [--all] [--json]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "profile list: unexpected operands")
		return 2
	}
	report, err := portage.ListProfiles(commandContext, *portageConfigRoot, *all)
	if err != nil {
		fmt.Fprintf(stderr, "profile list: %v\n", err)
		return 1
	}
	return writeProfileComparisonOutput(stdout, stderr, report, *jsonMode, func(writer io.Writer) {
		writeProfileCatalog(writer, report)
	})
}

func runProfileDiff(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("profile diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonMode := flags.Bool("json", *jsonOutput, "emit arise.profile-diff.v1")
	explain := flags.Bool("explain", *explainOutput, "include source contributions for changed settings")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: arise profile diff [--explain] [--json] [BEFORE] AFTER")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() < 1 || flags.NArg() > 2 {
		fmt.Fprintln(stderr, "profile diff: require TARGET or BEFORE AFTER; use REPOSITORY:PROFILE or active")
		return 2
	}
	before, after := "active", flags.Arg(0)
	if flags.NArg() == 2 {
		before, after = flags.Arg(0), flags.Arg(1)
	}
	report, err := portage.CompareProfiles(commandContext, *portageConfigRoot, before, after)
	if err != nil {
		fmt.Fprintf(stderr, "profile diff: %v\n", err)
		return 1
	}
	return writeProfileComparisonOutput(stdout, stderr, report, *jsonMode, func(writer io.Writer) {
		writeProfileDiff(writer, report, *explain)
	})
}

func writeProfileComparisonOutput(stdout, stderr io.Writer, document any, jsonMode bool, render func(io.Writer)) int {
	var output bytes.Buffer
	if jsonMode {
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			fmt.Fprintf(stderr, "profile: encode: %v\n", err)
			return 1
		}
	} else {
		render(&output)
	}
	if _, err := io.Copy(stdout, &output); err != nil {
		fmt.Fprintf(stderr, "profile: write: %v\n", err)
		return 1
	}
	return 0
}

func writeProfileCatalog(writer io.Writer, report portage.ProfileCatalog) {
	output := profileTextWriter{writer: writer, width: profileOutputWidth()}
	output.row("Architecture", report.Arch, 2, 12, color.BoldCyan, nil)
	if report.All {
		output.row("Filter", "all architectures", 2, 12, color.Bold, nil)
	}
	output.section("Advertised profiles", fmt.Sprintf("%d candidates", len(report.Profiles)))
	output.row("STATUS / ARCH", "PROFILE", 2, 23, color.Bold, color.Bold)
	for _, candidate := range report.Profiles {
		label := candidate.Status + " / " + candidate.Arch
		name := candidate.Selector
		style := color.Cyan
		if candidate.Deprecated {
			name += "  [deprecated]"
			style = color.Yellow
		}
		if candidate.Selected {
			name += "  [selected]"
			style = color.BoldGreen
		}
		output.row(label, name, 2, 23, nil, style)
	}
	if len(report.Profiles) == 0 {
		fmt.Fprintln(writer, "  (none)")
	}
	fmt.Fprintln(writer, "\nCompare with: arise profile diff REPOSITORY:PROFILE")
	fmt.Fprintln(writer, "Architecture matching filters candidates; transition requirements need separate assessment.")
}

func writeProfileDiff(writer io.Writer, report portage.ProfileDiff, explain bool) {
	output := profileTextWriter{writer: writer, width: profileOutputWidth(), sourceWidth: 42}
	output.row("Before", report.Before.Path, 2, 6, color.BoldCyan, color.Red)
	output.row("After", report.After.Path, 2, 6, color.BoldCyan, color.Green)
	fmt.Fprintln(writer, "\nProfile contributions only. '-' removes, '+' adds; the active profile is unchanged.")
	output.section("Inheritance", "changed layers")
	output.changedValues(report.Before.Layers, report.After.Layers, true)
	output.section("Defaults", fmt.Sprintf("%d changed", len(report.Defaults)))
	width := len("VARIABLE")
	for _, change := range report.Defaults {
		width = max(width, utf8.RuneCountInString(change.Name))
	}
	output.row("VARIABLE", "CHANGE", 2, width, color.Bold, color.Bold)
	for _, change := range report.Defaults {
		before, after := "(unset)", "(unset)"
		if change.Before != nil {
			before = change.Before.Value
		}
		if change.After != nil {
			after = change.After.Value
		}
		if change.Kind == "tokens" && change.Before != nil && change.After != nil {
			output.row(change.Name, "token changes", 2, width, color.Cyan, nil)
			output.changedValues(strings.Fields(before), strings.Fields(after), false)
		} else {
			output.row(change.Name, "- "+profileDiffValue(change.Before), 2, width, color.Cyan, color.Red)
			output.row("", "+ "+profileDiffValue(change.After), 2, width, nil, color.Green)
		}
		if explain {
			if change.Before != nil {
				fmt.Fprintln(writer, color.Bold("  Before sources:"))
				output.contributions(change.Before.Contributions, nil)
			}
			if change.After != nil {
				fmt.Fprintln(writer, color.Bold("  After sources:"))
				output.contributions(change.After.Contributions, nil)
			}
		}
	}
	output.section("Policy changes", fmt.Sprintf("%d sections", len(report.Policies)))
	for _, change := range report.Policies {
		label := "effective entries"
		if change.Kind == "ordered_rules" {
			label = "ordered rules"
		}
		output.section(change.Name, label)
		output.changedValues(change.Before, change.After, change.Kind == "ordered_rules")
		if explain {
			fmt.Fprintln(writer, color.Bold("  Before sources:"))
			output.contributions(change.BeforeSources, nil)
			fmt.Fprintln(writer, color.Bold("  After sources:"))
			output.contributions(change.AfterSources, nil)
		}
	}
	if len(report.Defaults) == 0 && len(report.Policies) == 0 {
		fmt.Fprintln(writer, "  No default or policy differences.")
	}
	for _, side := range []struct {
		name string
		data portage.ProfileDiffSide
	}{{"Before", report.Before}, {"After", report.After}} {
		if len(side.data.Unsupported) != 0 {
			name := side.name + " files not evaluated"
			if slices.Equal(report.Before.Unsupported, report.After.Unsupported) {
				if side.name == "After" {
					continue
				}
				name = "Files not evaluated"
			}
			output.section(name, "outside supported report policy")
			for _, path := range side.data.Unsupported {
				output.row("", path, 2, 0, nil, color.Yellow)
			}
		}
	}
}

func profileDiffValue(value *portage.ProfileDefault) string {
	if value == nil {
		return "(unset)"
	}
	return fmt.Sprintf("%q", value.Value)
}

func (output profileTextWriter) changedValues(before, after []string, ordered bool) {
	if ordered {
		prefix, suffix := 0, 0
		for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
			prefix++
		}
		for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
			suffix++
		}
		if prefix != 0 || suffix != 0 {
			output.row("", fmt.Sprintf("Unchanged entries surrounding this block: %d before, %d after", prefix, suffix), 2, 0, nil, nil)
		}
		before, after = before[prefix:len(before)-suffix], after[prefix:len(after)-suffix]
	} else {
		before, after = profileSetDifference(before, after), profileSetDifference(after, before)
	}
	for _, entry := range before {
		output.row("-", entry, 2, 1, color.Red, color.Red)
	}
	for _, entry := range after {
		output.row("+", entry, 2, 1, color.Green, color.Green)
	}
	if len(before) == 0 && len(after) == 0 {
		output.row("", "(none)", 2, 0, nil, nil)
	}
}

func profileSetDifference(before, after []string) []string {
	present := make(map[string]bool, len(after))
	for _, value := range after {
		present[value] = true
	}
	var result []string
	for _, value := range before {
		if !present[value] {
			result = append(result, value)
		}
	}
	return result
}
