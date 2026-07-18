package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/packagestate"
)

func runState(args []string, dbPath, vdbPath string) {
	mode := "json"
	if len(args) > 0 {
		mode = args[0]
	}
	if mode != "json" && mode != "fixture" && mode != "available" && mode != "installed" && mode != "available-cpv" && mode != "installed-cpv" {
		fmt.Fprintf(os.Stderr, "state: expected json, fixture, available, installed, available-cpv, or installed-cpv\n")
		return
	}
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state: open db: %v\n", err)
		return
	}
	defer db.Close()
	snapshot, err := packagestate.Capture(db, vdbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state: %v\n", err)
		return
	}
	switch mode {
	case "fixture":
		snapshot, err = snapshot.Portable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "state: make portable: %v\n", err)
			return
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(snapshot); err != nil {
			fmt.Fprintf(os.Stderr, "state: encode: %v\n", err)
		}
	case "available":
		for _, record := range snapshot.Available {
			fmt.Printf("%s::%s\n", record.CPV, record.Repository)
		}
	case "installed":
		for _, record := range snapshot.Installed {
			fmt.Printf("%s::%s\n", record.CPV, record.Repository)
		}
	case "available-cpv":
		for _, record := range snapshot.Available {
			fmt.Println(record.CPV)
		}
	case "installed-cpv":
		for _, record := range snapshot.Installed {
			fmt.Println(record.CPV)
		}
	default:
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(snapshot); err != nil {
			fmt.Fprintf(os.Stderr, "state: encode: %v\n", err)
		}
	}
}
