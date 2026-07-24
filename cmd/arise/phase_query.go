package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/installedquery"
)

// runPhaseQuery is an intentionally narrow, read-only subprocess interface
// used by the isolated Bash worker when a version query was not statically
// precomputed. It is handled before public CLI flag parsing.
func runPhaseQuery(args []string) int {
	if len(args) != 4 {
		fmt.Fprintln(os.Stderr, "phase query: expected operation, domain, atom and caller USE")
		return 2
	}
	operation, domain, query := args[0], args[1], args[2]
	var vdb string
	switch domain {
	case "b":
		vdb = os.Getenv("ARISE_QUERY_BROOT_VDB")
	case "d", "r":
		vdb = os.Getenv("ARISE_QUERY_ROOT_VDB")
	default:
		fmt.Fprintln(os.Stderr, "phase query: invalid dependency domain")
		return 2
	}
	if !filepath.IsAbs(vdb) {
		fmt.Fprintln(os.Stderr, "phase query: VDB path is not absolute")
		return 2
	}
	callerUse := make(map[string]bool)
	for _, flag := range strings.Fields(args[3]) {
		callerUse[flag] = true
	}
	switch operation {
	case "has-version":
		matched, err := installedquery.Match(vdb, query, callerUse)
		if err != nil {
			fmt.Fprintf(os.Stderr, "phase query: %v\n", err)
			return 2
		}
		if matched {
			fmt.Println("1")
		} else {
			fmt.Println("0")
		}
		return 0
	case "best-version":
		best, err := installedquery.Best(vdb, query, callerUse)
		if err != nil {
			fmt.Fprintf(os.Stderr, "phase query: %v\n", err)
			return 2
		}
		fmt.Println(best)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "phase query: invalid operation")
		return 2
	}
}
