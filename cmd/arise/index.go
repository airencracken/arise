package main

import (
	"fmt"
	"os"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/walker"
)

func runIndex(dbPath, repoPath string) {
	results, errs := walker.WalkCache(repoPath)
	db, err := ingest.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	var parseErrors int
	go func() {
		for e := range errs {
			parseErrors++
			if *verbose {
				fmt.Fprintf(os.Stderr, "index: %v\n", e)
			}
		}
	}()

	count, err := ingest.Ingest(db, results)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: ingest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("index: ingested %d packages", count)
	if parseErrors > 0 {
		fmt.Printf(" (%d non-fatal parse errors, use -v to see)", parseErrors)
	}
	fmt.Println()
}
