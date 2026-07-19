package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/preserved"
)

type preservedRepair struct {
	Package   string   `json:"package"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason"`
	Command   string   `json:"plan_command,omitempty"`
	Consumers []string `json:"elf_consumers,omitempty"`
	Closure   []string `json:"removal_closure,omitempty"`
}

func runPreservedRebuild() {
	if *jsonOutput && !*pretend {
		fmt.Fprintln(os.Stderr, "preserved-rebuild: --json requires --pretend")
		os.Exit(1)
	}
	if *pretend && !*jsonOutput {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	if !*jsonOutput {
		fmt.Println("Scanning for preserved-rebuild packages...")
	}

	reasons, err := preserved.RebuildReasons("/", *vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preserved-rebuild: %v\n", err)
		os.Exit(1)
	}

	packages := uniqueRebuildPackages(reasons)
	repairs := unavailablePreservedRepairs(packages)
	if *jsonOutput {
		document := map[string]any{"schema": 1, "operation": "preserved-rebuild-scan", "complete": true, "packages": packages, "reasons": reasons, "repair_proposals": repairs}
		if err := json.NewEncoder(os.Stdout).Encode(document); err != nil {
			fmt.Fprintf(os.Stderr, "preserved-rebuild: encode JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(packages) == 0 {
		fmt.Println("No packages need to be rebuilt (no broken preserved links).")
		return
	}

	fmt.Printf("\nThe following %d package(s) need to be rebuilt:\n", len(packages))
	for _, pkg := range packages {
		fmt.Printf("  %s\n", pkg)
	}
	if len(repairs) > 0 {
		fmt.Println("\nUnavailable preserved consumers require an explicit repair before rebuilding:")
		for _, repair := range repairs {
			fmt.Printf("  %s: %s\n", repair.Package, repair.Reason)
			if repair.Command != "" {
				fmt.Printf("    verify and freeze removal: %s\n", repair.Command)
			} else if len(repair.Consumers) > 0 {
				fmt.Printf("    removal blocked by ELF consumers: %s\n", strings.Join(repair.Consumers, ", "))
			}
		}
		fmt.Println("Removal remains disabled until the verified plan SHA-256 is explicitly approved.")
	}

	if *pretend {
		return
	}
	fmt.Fprintln(os.Stderr, unsupportedRebuildMessage("preserved-rebuild"))
	os.Exit(1)
}

func unavailablePreservedRepairs(packages []string) []preservedRepair {
	var repairs []preservedRepair
	for _, installedCPV := range packages {
		category, packageName, version, err := metadata.ParseCPV(installedCPV)
		if err != nil || version == "" {
			continue
		}
		available := false
		patterns := []string{
			filepath.Join(*repoPath, category, packageName, "*.ebuild"),
			filepath.Join("/var/db/repos", "*", category, packageName, "*.ebuild"),
		}
		for _, pattern := range patterns {
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				available = true
				break
			}
		}
		if available {
			continue
		}
		exact := "=" + installedCPV
		consumers, consumerErr := preserved.ReverseELFConsumers(*vdbDir, installedCPV)
		if consumerErr == nil && len(consumers) > 0 {
			closure, closureErr := preserved.ReverseELFRemovalClosure(*vdbDir, installedCPV)
			if closureErr == nil && removableUnavailableClosure(closure) {
				targets := make([]string, 0, len(closure))
				for _, cpv := range closure {
					targets = append(targets, "="+cpv)
				}
				repairs = append(repairs, preservedRepair{
					Package: installedCPV, Action: "evaluate-removal-closure",
					Reason:    "no current ebuild; native ELF consumers form an unselected unavailable retirement closure",
					Consumers: consumers, Closure: closure,
					Command: "arise --pretend --json uninstall " + strings.Join(targets, " "),
				})
				continue
			}
			repairs = append(repairs, preservedRepair{
				Package: installedCPV, Action: "replacement-required",
				Reason: "no current ebuild; removal would break installed ELF consumers", Consumers: consumers,
			})
			continue
		}
		repairs = append(repairs, preservedRepair{
			Package: installedCPV,
			Action:  "evaluate-removal",
			Reason:  "installed preserved-library consumer has no ebuild in configured repositories",
			Command: "arise --pretend --json uninstall " + exact,
		})
	}
	sort.Slice(repairs, func(i, j int) bool { return strings.Compare(repairs[i].Package, repairs[j].Package) < 0 })
	return repairs
}

func removableUnavailableClosure(closure []string) bool {
	worldData, err := os.ReadFile(*worldFile)
	if err != nil {
		return false
	}
	selected := make(map[string]bool)
	for _, line := range strings.Split(string(worldData), "\n") {
		selected[strings.TrimSpace(line)] = true
	}
	for _, cpv := range closure {
		category, packageName, _, err := metadata.ParseCPV(cpv)
		if err != nil || selected[category+"/"+packageName] {
			return false
		}
		installedSlotData, err := os.ReadFile(filepath.Join(*vdbDir, filepath.FromSlash(cpv), "SLOT"))
		if err != nil {
			return false
		}
		installedSlot := strings.SplitN(strings.TrimSpace(string(installedSlotData)), "/", 2)[0]
		if repositoryHasSlot(category, packageName, installedSlot) {
			return false
		}
	}
	return true
}

func repositoryHasSlot(category, packageName, installedSlot string) bool {
	patterns := []string{filepath.Join(*repoPath, category, packageName, "*.ebuild"), filepath.Join("/var/db/repos", "*", category, packageName, "*.ebuild")}
	seen := make(map[string]bool)
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true
			repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(path)))
			cache := filepath.Join(repoRoot, "metadata", "md5-cache", category, strings.TrimSuffix(filepath.Base(path), ".ebuild"))
			cacheKnown := false
			if data, err := os.ReadFile(cache); err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					if strings.HasPrefix(line, "SLOT=") {
						cacheKnown = true
						slot := strings.SplitN(strings.TrimPrefix(line, "SLOT="), "/", 2)[0]
						if slot == installedSlot {
							return true
						}
						break
					}
				}
			}
			if cacheKnown {
				continue
			}
			parsed, err := ebuild.ParseEbuild(path)
			if err != nil {
				return true
			}
			raw, ok := parsed.Variables["SLOT"]
			if !ok {
				return installedSlot == "0"
			}
			slot := strings.SplitN(strings.Trim(strings.TrimSpace(raw), "\"'"), "/", 2)[0]
			if strings.ContainsAny(slot, "$`(){}") {
				return true
			}
			if slot == installedSlot {
				return true
			}
		}
	}
	return false
}

func uniqueRebuildPackages(reasons []preserved.RebuildReason) []string {
	seen := make(map[string]bool)
	packages := make([]string, 0)
	for _, reason := range reasons {
		if !seen[reason.Package] {
			seen[reason.Package] = true
			packages = append(packages, reason.Package)
		}
	}
	return packages
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
