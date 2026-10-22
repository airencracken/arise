package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

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
