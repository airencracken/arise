package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/world"
)

func runDepclean(dbPath, repoDir string) {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: build graph: %v\n", err)
		os.Exit(1)
	}

	rg := g.ToResolveGraph()

	w, err := world.LoadWorld(*worldFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: load world: %v\n", err)
		os.Exit(1)
	}

	ws := &resolve.WorldSet{Entries: w.Atoms}

	removals, err := resolve.Depclean(rg, ws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: %v\n", err)
		os.Exit(1)
	}

	if len(removals) == 0 {
		fmt.Println("\nNothing to remove.")
		return
	}

	fmt.Printf("\nProposed removals (%d packages):\n", len(removals))
	for _, r := range removals {
		fmt.Printf("  [%s] %s  (reason: %s)\n", r.Action, r.Atom, r.Reason)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to remove these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}
}

func runPrune(dbPath, repoDir string) {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune: build graph: %v\n", err)
		os.Exit(1)
	}

	rg := g.ToResolveGraph()

	removals, err := resolve.Prune(rg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune: %v\n", err)
		os.Exit(1)
	}

	if len(removals) == 0 {
		fmt.Println("\nNothing to prune.")
		return
	}

	fmt.Printf("\nProposed removals (%d packages):\n", len(removals))
	for _, r := range removals {
		fmt.Printf("  [%s] %s  (reason: %s)\n", r.Action, r.Atom, r.Reason)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to remove these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}
}
