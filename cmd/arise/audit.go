package main

import (
	"context"
	"fmt"
	"os"

	"github.com/airencracken/arise/internal/audit"
)

func runAudit(args []string, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "audit: expected subcommand: python or perl\n")
		fmt.Fprintf(os.Stderr, "Usage: arise audit <python|perl> [--fix] [--pretend] [--jobs N]\n")
		os.Exit(1)
	}

	auditType := args[0]
	auditArgs := args[1:]

	fix := false
	pretend := false
	jobs := 0

	for i := 0; i < len(auditArgs); i++ {
		switch auditArgs[i] {
		case "--fix":
			fix = true
		case "--pretend":
			pretend = true
		case "--jobs":
			if i+1 < len(auditArgs) {
				fmt.Sscanf(auditArgs[i+1], "%d", &jobs)
				i++
			}
		}
	}

	vdbPath := *vdbDir

	var results []audit.VdbAuditResult
	var err error

	switch auditType {
	case "python":
		results, err = audit.AuditPython(vdbPath)
	case "perl":
		results, err = audit.AuditPerl(vdbPath)
	default:
		fmt.Fprintf(os.Stderr, "audit: unknown subcommand %q; expected python or perl\n", auditType)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "audit %s: %v\n", auditType, err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Printf("No outdated %s paths found.\n", auditType)
		return
	}

	var packages []string
	for _, r := range results {
		fmt.Printf("\nPackage: %s\n", r.PackagePath)
		fmt.Printf("  Old versions: %v\n", r.OldVersions)
		for _, c := range r.AffectedContents {
			fmt.Printf("    %s\n", c)
		}
		if fix {
			atoms := vdbPathToAtoms(r.PackagePath)
			packages = append(packages, atoms...)
		}
	}

	if fix {
		fmt.Printf("\nRebuilding %d packages...\n", len(packages))
		cfg := buildRebuildConfig(repoDir, jobs, func(phase string) {
			fmt.Printf("  [%s]\n", phase)
		}, func(phase string, err error) {
			if err != nil {
				fmt.Printf("  [%s] FAILED: %v\n", phase, err)
			}
		})
		if pretend {
			fmt.Println("(pretend mode: would rebuild these packages)")
			return
		}
		loadAvg := *loadAverage
		if err := runRebuild(context.Background(), packages, cfg, jobs, loadAvg); err != nil {
			fmt.Fprintf(os.Stderr, "rebuild: %v\n", err)
			os.Exit(1)
		}
	}
}
