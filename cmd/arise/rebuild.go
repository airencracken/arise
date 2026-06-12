package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/preserved"
)

func runPreservedRebuild() {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	fmt.Println("Scanning for preserved-rebuild packages...")

	packages, err := preserved.RebuildNeeded("/", *vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preserved-rebuild: %v\n", err)
		os.Exit(1)
	}

	if len(packages) == 0 {
		fmt.Println("No packages need to be rebuilt (no broken preserved links).")
		return
	}

	fmt.Printf("\nThe following %d package(s) need to be rebuilt:\n", len(packages))
	for _, pkg := range packages {
		fmt.Printf("  %s\n", pkg)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to rebuild these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}

	jobs := *jobsVal
	if jobs <= 0 {
		jobs = 1
	}

	cfg := buildRebuildConfig(*repoPath, jobs, func(phase string) {
		fmt.Printf("  [%s]\n", phase)
	}, func(phase string, err error) {
		if err != nil {
			fmt.Printf("  [%s] FAILED: %v\n", phase, err)
		}
	})

	loadAvg := *loadAverage
	if err := runRebuild(context.Background(), packages, cfg, jobs, loadAvg); err != nil {
		fmt.Fprintf(os.Stderr, "preserved-rebuild: %v\n", err)
		os.Exit(1)
	}
}

func runRevdepRebuild() {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	fmt.Println("Scanning for broken reverse dependencies...")

	packages, err := preserved.RevdepRebuild("/", *vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "revdep-rebuild: %v\n", err)
		os.Exit(1)
	}

	if len(packages) == 0 {
		fmt.Println("No packages with broken dependencies found.")
		return
	}

	fmt.Printf("\nThe following %d package(s) have broken dependencies:\n", len(packages))
	for _, pkg := range packages {
		fmt.Printf("  %s\n", pkg)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to rebuild these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}

	jobs := *jobsVal
	if jobs <= 0 {
		jobs = 1
	}

	cfg := buildRebuildConfig(*repoPath, jobs, func(phase string) {
		fmt.Printf("  [%s]\n", phase)
	}, func(phase string, err error) {
		if err != nil {
			fmt.Printf("  [%s] FAILED: %v\n", phase, err)
		}
	})

	loadAvg := *loadAverage
	if err := runRebuild(context.Background(), packages, cfg, jobs, loadAvg); err != nil {
		fmt.Fprintf(os.Stderr, "revdep-rebuild: %v\n", err)
		os.Exit(1)
	}
}
