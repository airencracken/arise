package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/airencracken/arise/internal/configdoctor"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/world"
)

func runDoctor(args []string, dbPath string) int {
	if len(args) != 1 || (args[0] != "package-use" && args[0] != "package-policy" && args[0] != "world" && args[0] != "all") {
		fmt.Fprintln(os.Stderr, "doctor: expected package-use, package-policy, world, or all")
		return 2
	}
	config, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: load Portage configuration: %v\n", err)
		return 1
	}
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: open resolver index: %v\n", err)
		return 1
	}
	defer db.Close()
	state, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: build package state: %v\n", err)
		return 1
	}
	resolverGraph := state.ToResolveGraph()
	var worldEntries []string
	if args[0] == "world" || args[0] == "all" {
		selected, loadErr := world.LoadWorld(*worldFile)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "doctor: load world set: %v\n", loadErr)
			return 1
		}
		worldEntries = selected.Atoms
	}
	var report configdoctor.Report
	switch args[0] {
	case "package-use":
		report = configdoctor.PackageUse(config, resolverGraph)
	case "package-policy":
		report = configdoctor.PackagePolicy(config, resolverGraph)
	case "world":
		report = configdoctor.WorldTargets(worldEntries, resolverGraph)
	default:
		report = configdoctor.All(config, resolverGraph, worldEntries)
	}
	if err := writeDoctorReport(os.Stdout, report, *jsonOutput); err != nil {
		fmt.Fprintf(os.Stderr, "doctor: write report: %v\n", err)
		return 1
	}
	return 0
}

func writeDoctorReport(writer io.Writer, report configdoctor.Report, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	if len(report.Findings) == 0 {
		_, err := fmt.Fprintln(writer, "No configuration findings.")
		return err
	}
	for _, finding := range report.Findings {
		location := fmt.Sprintf("rule %d", finding.Rule)
		if finding.Related != 0 {
			location += fmt.Sprintf(", related rule %d", finding.Related)
		}
		flag := ""
		if finding.Flag != "" {
			flag = " flag=" + finding.Flag
		}
		if _, err := fmt.Fprintf(writer, "%s: %s: %s: %s (%s atom=%s%s)\n", finding.Severity, finding.Family, finding.Kind, finding.Message, location, finding.Atom, flag); err != nil {
			return err
		}
	}
	return nil
}
