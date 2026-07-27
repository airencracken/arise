package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/recoveryset"
)

func runRecover(args []string) {
	if len(args) == 2 && args[0] == "verify-set" {
		bootID, err := recoveryset.CurrentBootID()
		if err == nil {
			err = recoveryset.VerifyAfterReboot(args[1], bootID)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover: verify set: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Verified recovery set %s after a subsequent boot.\n", args[1])
		return
	}
	if len(args) == 2 && args[0] == "prune-sets" {
		keep, err := strconv.Atoi(args[1])
		if err != nil || keep < 0 {
			fmt.Fprintln(os.Stderr, "recover: retained verified count must be a non-negative integer")
			os.Exit(2)
		}
		result, err := recoveryset.PruneVerified(filepath.Join(*binpkgDir, ".arise-recovery"), keep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover: prune sets: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Pruned %d verified recovery set(s) and %d unreferenced object(s); preserved %d set(s).\n",
			len(result.Removed), len(result.RemovedObjects), len(result.Preserved))
		return
	}
	if len(args) == 2 && (args[0] == "inspect-set" || args[0] == "restore-set") {
		configurationFingerprint, err := binpkg.FingerprintConfiguration(*portageConfigRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover: fingerprint Portage configuration: %v\n", err)
			os.Exit(1)
		}
		repositoryFingerprint, err := binpkg.FingerprintRepositoryIdentity(*repoPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover: fingerprint repository identity: %v\n", err)
			os.Exit(1)
		}
		root := commandEnv("ROOT", "/")
		request := recoveryset.RestoreRequest{
			SetPath: args[1], RootDir: root, VDBDir: *vdbDir, JournalDir: *journalDir,
			WorkDir:              filepath.Join(commandEnv("PORTAGE_TMPDIR", "/var/tmp"), "arise", "restore"),
			CurrentConfiguration: configurationFingerprint, CurrentRepository: repositoryFingerprint,
			ApprovedDriftSHA256: *approveRecoveryDriftSHA256,
			AllowLiveRoot:       filepath.Clean(root) == string(filepath.Separator),
		}
		if args[0] == "inspect-set" {
			plan, err := recoveryset.InspectRestore(request)
			if err != nil {
				fmt.Fprintf(os.Stderr, "recover: inspect set: %v\n", err)
				os.Exit(1)
			}
			if *jsonOutput {
				if err := json.NewEncoder(os.Stdout).Encode(plan); err != nil {
					fmt.Fprintf(os.Stderr, "recover: encode restore plan: %v\n", err)
					os.Exit(1)
				}
				return
			}
			fmt.Printf("Recovery set %s: %d artifact(s), reverse capture order\n", plan.SetID, len(plan.Artifacts))
			for _, artifact := range plan.Artifacts {
				fmt.Printf("  %s\n", artifact.Identity)
			}
			for _, drift := range plan.Drift {
				fmt.Printf("Drift: %s\n", drift)
			}
			if plan.DriftApprovalSHA256 != "" {
				fmt.Printf("Drift approval SHA-256: %s\n", plan.DriftApprovalSHA256)
			}
			return
		}
		if err := recoveryset.Restore(context.Background(), request); err != nil {
			fmt.Fprintf(os.Stderr, "recover: restore set: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Restored recovery set %s; verification remains pending.\n", args[1])
		return
	}
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
		fmt.Fprintln(os.Stderr, "Usage: arise recover status | arise recover rollback <journal-id> | arise recover rollback --all-active | arise recover inspect-set <path> | arise recover restore-set <path> | arise recover verify-set <path> | arise recover prune-sets <keep>")
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
