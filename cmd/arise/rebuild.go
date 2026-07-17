package main

import (
	"fmt"
	"os"

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
	fmt.Fprintln(os.Stderr, unsupportedRebuildMessage("preserved-rebuild"))
	os.Exit(1)
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
	fmt.Fprintln(os.Stderr, unsupportedRebuildMessage("revdep-rebuild"))
	os.Exit(1)
}

func unsupportedRebuildMessage(command string) string {
	return fmt.Sprintf("arise: %s execution is experimental and unavailable; rerun with --pretend (live rebuild remains gated on the P4/P6 transaction engine)", command)
}
