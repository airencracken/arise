package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/oplock"
)

func runRecover(args []string) {
	if len(args) == 0 || args[0] == "status" {
		summaries, err := journal.List(*journalDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover: %v\n", err)
			os.Exit(1)
		}
		if *jsonOutput {
			if err := json.NewEncoder(os.Stdout).Encode(summaries); err != nil {
				fmt.Fprintf(os.Stderr, "recover: encode status: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if len(summaries) == 0 {
			fmt.Println("No operation journals found.")
			return
		}
		for _, summary := range summaries {
			fmt.Printf("%s\t%s\troot=%s\tentries=%d\n", summary.ID, summary.Status, summary.Root, summary.Entries)
		}
		return
	}
	if args[0] != "rollback" || len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: arise recover status | arise recover rollback <journal-id> | arise recover rollback --all-active")
		os.Exit(2)
	}
	lock, err := oplock.TryAcquireVDB(*vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recover: acquire VDB lock: %v\n", err)
		os.Exit(1)
	}
	defer lock.Release()
	if args[1] == "--all-active" {
		recovered, err := journal.RecoverActive(*journalDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Recovered %d active operation journals.\n", len(recovered))
		return
	}
	summary, err := journal.RollbackActive(*journalDir, args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "recover: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Recovered %s (%d entries); status=%s\n", summary.ID, summary.Entries, summary.Status)
}
