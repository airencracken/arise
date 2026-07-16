package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/airencracken/arise/internal/perf"
)

func main() {
	workloadPath := flag.String("workload", "", "JSON workload containing paired Arise and Portage commands")
	snapshot := flag.String("snapshot", "", "identity of the shared repository/profile/VDB snapshot")
	output := flag.String("output", "", "report path (stdout when empty)")
	timeout := flag.Duration("timeout", 30*time.Minute, "total benchmark timeout")
	flag.Parse()
	if *workloadPath == "" || *snapshot == "" {
		fmt.Fprintln(os.Stderr, "arise-perf: -workload and -snapshot are required")
		os.Exit(2)
	}
	w, err := perf.LoadWorkload(*workloadPath)
	if err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := perf.Run(ctx, w, *snapshot)
	if err != nil {
		fail(err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fail(err)
	}
	data = append(data, '\n')
	if *output == "" {
		_, err = os.Stdout.Write(data)
	} else {
		err = os.WriteFile(*output, data, 0644)
	}
	if err != nil {
		fail(err)
	}
	if !report.AllPassed {
		os.Exit(1)
	}
}

func fail(err error) { fmt.Fprintf(os.Stderr, "arise-perf: %v\n", err); os.Exit(1) }
