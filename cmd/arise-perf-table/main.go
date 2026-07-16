package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/airencracken/arise/internal/perf"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: arise-perf-table REPORT.json [REPORT.json ...]")
		os.Exit(2)
	}
	var reports []perf.Report
	for _, path := range os.Args[1:] {
		data, err := os.ReadFile(path)
		if err != nil {
			fail(path, err)
		}
		var report perf.Report
		if err := json.Unmarshal(data, &report); err != nil {
			fail(path, err)
		}
		reports = append(reports, report)
	}
	fmt.Print(perf.MarkdownMatrix(reports))
}

func fail(path string, err error) {
	fmt.Fprintf(os.Stderr, "arise-perf-table: %s: %v\n", path, err)
	os.Exit(1)
}
