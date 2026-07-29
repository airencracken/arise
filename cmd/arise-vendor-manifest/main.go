package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/airencracken/arise/internal/vendorartifact"
)

func main() {
	mode := flag.String("mode", "create", "create or verify")
	root := flag.String("root", ".", "source root")
	output := flag.String("output", "", "manifest output path")
	version := flag.String("version", "", "release version")
	commit := flag.String("commit", "", "source commit")
	epoch := flag.Int64("source-date-epoch", 0, "source timestamp")
	flag.Parse()

	if *mode == "verify" {
		file, err := os.Open(*output)
		if err != nil {
			fail(err)
		}
		manifest, err := vendorartifact.Decode(file)
		_ = file.Close()
		if err == nil {
			err = vendorartifact.VerifyIdentity(manifest, *version, *commit)
		}
		if err == nil {
			err = vendorartifact.Verify(*root, manifest)
		}
		if err != nil {
			fail(err)
		}
		return
	}
	if *mode != "create" || *output == "" {
		fail(fmt.Errorf("create requires -output; mode must be create or verify"))
	}
	manifest, err := vendorartifact.Create(*root, *version, *commit, runtime.Version(), *epoch)
	if err != nil {
		fail(err)
	}
	file, err := os.Create(*output)
	if err != nil {
		fail(err)
	}
	if err := vendorartifact.Encode(file, manifest); err != nil {
		_ = file.Close()
		fail(err)
	}
	if err := file.Close(); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "vendor manifest:", err)
	os.Exit(1)
}
