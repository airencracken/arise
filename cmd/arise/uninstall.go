package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/atom"
)

func runUninstall(args []string, dbPath, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "uninstall: missing package atom arguments\n")
		os.Exit(1)
	}

	for _, arg := range args {
		a, err := atom.Parse(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: parsing atom %q: %v\n", arg, err)
			continue
		}

		vdbPath := filepath.Join(*vdbDir, a.Category, a.Package)
		if a.Version != nil && a.Version.Raw != "" {
			vdbPath = vdbPath + "-" + a.Version.Raw
		}

		fmt.Printf("Uninstall: %s\n", vdbPath)
	}
}
