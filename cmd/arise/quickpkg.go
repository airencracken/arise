package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
)

func runQuickPkg(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "quickpkg: missing package atom argument\n")
		fmt.Fprintf(os.Stderr, "Usage: arise quickpkg <atom>\n")
		os.Exit(1)
	}

	a, err := atom.Parse(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: parsing atom %q: %v\n", args[0], err)
		os.Exit(1)
	}

	vdbPath := filepath.Join(*vdbDir, a.Category, a.Package)
	if a.Version != nil && a.Version.Raw != "" {
		vdbPath = vdbPath + "-" + a.Version.Raw
	}

	ctx := context.Background()
	outPath, err := binpkg.Create(ctx, vdbPath, "/", *binpkgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(outPath)
}
