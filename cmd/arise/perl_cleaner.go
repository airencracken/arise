package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/perlcleaner"
	"github.com/airencracken/arise/internal/world"
)

type perlCleanerOptions struct {
	Mode            perlcleaner.Mode
	Pretend         bool
	DeleteLeftovers bool
}

var errPerlCleanerHelp = errors.New("help requested")

func parsePerlCleanerOptions(args []string) (perlCleanerOptions, error) {
	options := perlCleanerOptions{DeleteLeftovers: true}
	selected := 0
	for _, arg := range args {
		switch arg {
		case "--modules", "modules":
			options.Mode = perlcleaner.ModulesMode()
			selected++
		case "--allmodules", "allmodules":
			options.Mode = perlcleaner.AllModulesMode()
			selected++
		case "--libperl", "libperl":
			options.Mode = perlcleaner.LibPerlMode()
			selected++
		case "--all", "all":
			options.Mode = perlcleaner.AllMode()
			selected++
		case "--reallyall", "reallyall":
			options.Mode = perlcleaner.ReallyAllMode()
			selected++
		case "-p", "--pretend", "--dry-run":
			options.Pretend = true
		case "--dont-delete-leftovers", "dont-delete-leftovers":
			options.DeleteLeftovers = false
		case "-h", "--help":
			return options, errPerlCleanerHelp
		default:
			return options, fmt.Errorf("unknown option %q", arg)
		}
	}
	if selected != 1 {
		return options, fmt.Errorf("require exactly one of --modules, --allmodules, --libperl, --all, or --reallyall")
	}
	return options, nil
}

func runPerlCleaner(args []string) int {
	options, err := parsePerlCleanerOptions(args)
	if err != nil {
		printPerlCleanerUsage(os.Stderr)
		if errors.Is(err, errPerlCleanerHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "perl-cleaner: %v\n", err)
		return 2
	}
	report, err := perlcleaner.Check(*vdbDir, options.Mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "perl-cleaner: %v\n", err)
		return 1
	}
	targets := perlcleaner.Targets(report)
	leftovers, err := perlcleaner.FindLeftovers(commandEnv("ROOT", "/"), report.ABI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "perl-cleaner: inspect leftovers: %v\n", err)
		return 1
	}
	if *jsonOutput {
		document := struct {
			Schema    int                    `json:"schema"`
			Operation string                 `json:"operation"`
			Complete  bool                   `json:"complete"`
			Report    perlcleaner.Report     `json:"report"`
			Targets   []string               `json:"targets"`
			Leftovers []perlcleaner.Leftover `json:"leftovers"`
		}{Schema: 1, Operation: "perl-cleaner", Complete: true, Report: report, Targets: targets, Leftovers: leftovers}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			fmt.Fprintf(os.Stderr, "perl-cleaner: encode report: %v\n", err)
			return 1
		}
		return 0
	}
	printPerlCleanerReport(os.Stdout, report, options, leftovers)
	if len(targets) == 0 {
		fmt.Fprintln(os.Stdout, "No package needs to be reinstalled.")
	}
	if options.Pretend {
		*pretend = true
	}
	if *pretend && len(targets) == 0 {
		return 0
	}
	if !*pretend && report.Mode.Preclean && len(targets) != 0 {
		if err := deselectPerlCore(report.Preclean.PerlCore); err != nil {
			fmt.Fprintf(os.Stderr, "perl-cleaner: update world set: %v\n", err)
			return 1
		}
	}
	if len(targets) != 0 {
		cfg := resolveFlagsToConfig(report.Mode.Preclean, false)
		cfg.Reinstall, cfg.ExplicitReinstall = true, true
		cfg.Oneshot, cfg.CompleteGraph, cfg.Pretend = true, true, *pretend
		runResolve(targets, *dbPath, *repoPath, cfg)
	}
	if *pretend {
		return 0
	}
	after, err := perlcleaner.Check(*vdbDir, perlcleaner.FinalValidationMode(options.Mode))
	if err != nil {
		fmt.Fprintf(os.Stderr, "perl-cleaner: final-state validation failed: %v\n", err)
		return 1
	}
	if len(after.Actions) != 0 {
		fmt.Fprintf(os.Stderr, "perl-cleaner: final-state validation found %d unresolved package(s)\n", len(after.Actions))
		return 1
	}
	remaining, err := perlcleaner.FindLeftovers(commandEnv("ROOT", "/"), after.ABI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "perl-cleaner: final leftover scan failed: %v\n", err)
		return 1
	}
	if options.DeleteLeftovers {
		if err := perlcleaner.DeleteKnown(commandEnv("ROOT", "/"), *journalDir, remaining); err != nil {
			fmt.Fprintf(os.Stderr, "perl-cleaner: delete known leftovers: %v\n", err)
			return 1
		}
		remaining, err = perlcleaner.FindLeftovers(commandEnv("ROOT", "/"), after.ABI)
		if err != nil {
			fmt.Fprintf(os.Stderr, "perl-cleaner: verify leftovers: %v\n", err)
			return 1
		}
	}
	for _, leftover := range remaining {
		fmt.Fprintf(os.Stdout, "Perl leftover requires manual review: %s\n", leftover.Path)
	}
	fmt.Fprintln(os.Stdout, "Perl repair completed and final state validated.")
	return 0
}

func printPerlCleanerUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: arise perl-cleaner --modules|--allmodules|--libperl|--all|--reallyall [--pretend] [--dont-delete-leftovers]")
}

func printPerlCleanerReport(writer io.Writer, report perlcleaner.Report, options perlCleanerOptions, leftovers []perlcleaner.Leftover) {
	fmt.Fprintf(writer, "Active Perl ABI: %s", report.ABI.Version)
	if report.ABI.Arch != "" {
		fmt.Fprintf(writer, " (%s)", report.ABI.Arch)
	}
	fmt.Fprintf(writer, "; libperl %s\n", strings.Join(report.ABI.LibPerlSONames, ", "))
	if report.Mode.Preclean {
		fmt.Fprintf(writer, "Pre-clean: %d perl-core world candidate(s), %d installed virtual(s)\n",
			len(report.Preclean.PerlCore), len(report.Preclean.Virtuals))
	}
	for _, action := range report.Actions {
		fmt.Fprintf(writer, "  rebuild %s as %s\n", action.CPV, action.Atom)
		for _, reason := range action.Reasons {
			fmt.Fprintf(writer, "    %s: %s\n", reason.Kind, reason.Evidence)
		}
	}
	if report.Mode.Leftovers {
		if options.DeleteLeftovers {
			fmt.Fprintf(writer, "Leftover-file cleanup: %d candidate(s); known generated files will be deleted after package repair.\n", len(leftovers))
		} else {
			fmt.Fprintf(writer, "Leftover-file cleanup: report only (%d candidate(s)).\n", len(leftovers))
		}
	}
}

func deselectPerlCore(core []string) error {
	remove := make(map[string]bool, len(core))
	for _, cp := range core {
		remove[cp] = true
	}
	return world.Update(*worldFile, func(set *world.WorldSet) error {
		result := set.Atoms[:0]
		for _, entry := range set.Atoms {
			parsed, err := atom.Parse(entry)
			if err == nil && remove[parsed.CP()] {
				continue
			}
			result = append(result, entry)
		}
		set.Atoms = result
		return nil
	})
}
