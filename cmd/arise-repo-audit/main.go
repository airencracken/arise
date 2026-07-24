// arise-repo-audit performs a read-only structural and compatibility audit of
// an entire Portage repository.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/airencracken/arise/internal/repoaudit"
)

func main() {
	repository := flag.String("repo", "/var/db/repos/gentoo", "Portage repository root")
	worker := flag.String("worker", "internal/phaseproto/worker.sh", "Arise phase worker to compare with package-manager helper usage")
	output := flag.String("output", "", "write the full JSON report to this path")
	jsonOnly := flag.Bool("json", false, "write JSON to stdout instead of a summary")
	flag.Parse()

	report, err := repoaudit.Run(*repository, *worker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "arise-repo-audit: %v\n", err)
		os.Exit(1)
	}
	if *output != "" {
		if err := writeJSON(*output, report); err != nil {
			fmt.Fprintf(os.Stderr, "arise-repo-audit: %v\n", err)
			os.Exit(1)
		}
	}
	if *jsonOnly {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "arise-repo-audit: %v\n", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("Repository: %s\n", report.Repository)
	fmt.Printf("Scanned: %d ebuilds, %d eclasses\n", report.Ebuilds, report.Eclasses)
	fmt.Printf("Parse failures: %d\n", len(report.ParseFailures))
	fmt.Printf("Missing static inherits: %d\n", len(report.MissingInherits))
	fmt.Printf("Parser/static inherit differences: %d\n", len(report.InheritDifferences))
	fmt.Printf("Eclass cycles: %d\n", len(report.InheritCycles))
	fmt.Printf("Version queries: %d", len(report.Queries))
	var classes []string
	for class := range report.QueryClasses {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		fmt.Printf("  %s=%d", class, report.QueryClasses[class])
	}
	fmt.Println()
	var absent []repoaudit.HelperCoverage
	for _, helper := range report.HelperCoverage {
		if helper.Uses > 0 && !helper.Implemented {
			absent = append(absent, helper)
		}
	}
	fmt.Printf("Used package-manager helpers absent from worker: %d\n", len(absent))
	for _, helper := range absent {
		fmt.Printf("  %s (%d references)\n", helper.Helper, helper.Uses)
	}
	if *output != "" {
		absolute, _ := filepath.Abs(*output)
		fmt.Printf("Full JSON report: %s\n", absolute)
	}
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
