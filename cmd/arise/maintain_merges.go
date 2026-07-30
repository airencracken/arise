package main

import (
	"fmt"
	"os"

	"github.com/airencracken/arise/internal/mergemaint"
	"github.com/airencracken/arise/internal/oplock"
)

func failedMergesPath() string {
	return commandRootPath("/var/lib/portage/failed-merges")
}

func runMaintainMerges(args []string) int {
	check, fix, purge := false, false, false
	for _, arg := range args {
		switch arg {
		case "--check":
			check = true
		case "--fix":
			fix = true
		case "--purge":
			purge = true
		case "-h", "--help":
			fmt.Println("Usage: arise [global options] maintain merges --check|--fix|--purge")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "maintain merges: unknown option %q\n", arg)
			return 2
		}
	}
	if countTrue(check, fix, purge) != 1 {
		fmt.Fprintln(os.Stderr, "maintain merges: require exactly one of --check, --fix, or --purge")
		return 2
	}
	tracking := failedMergesPath()
	if purge {
		if *pretend {
			fmt.Fprintf(os.Stdout, "Would purge failed-merge recovery tracking: %s\n", tracking)
			return 0
		}
		trackingLock, err := oplock.TryAcquirePath(tracking)
		if err != nil {
			fmt.Fprintf(os.Stderr, "maintain merges: acquire tracking lock: %v\n", err)
			return 1
		}
		defer trackingLock.Release()
		if err := mergemaint.PurgeTracking(tracking); err != nil {
			fmt.Fprintf(os.Stderr, "maintain merges: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "Purged failed-merge recovery tracking.")
		return 0
	}
	report, err := mergemaint.Check(*vdbDir, tracking)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: %v\n", err)
		return 1
	}
	if check {
		printFailedMerges(report)
		if len(report.Failed) != 0 {
			return 1
		}
		return 0
	}
	if len(report.Failed) == 0 {
		fmt.Fprintln(os.Stdout, "No failed merges found.")
		return 0
	}
	if *pretend {
		fmt.Fprintf(os.Stdout, "Failed-merge recovery plan (%d package(s)):\n", len(report.Failed))
		for _, failed := range report.Failed {
			fmt.Fprintf(os.Stdout, "  remove %s; rebuild %s\n", failed.Entry, failed.Atom)
		}
		return 0
	}
	lock, err := oplock.TryAcquireVDB(*vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: acquire VDB lock: %v\n", err)
		return 1
	}
	defer lock.Release()
	trackingLock, err := oplock.TryAcquirePath(tracking)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: acquire tracking lock: %v\n", err)
		return 1
	}
	defer trackingLock.Release()
	lockedReport, checkErr := mergemaint.Check(*vdbDir, tracking)
	if checkErr != nil || !sameFailedMerges(report, lockedReport) {
		fmt.Fprintln(os.Stderr, "maintain merges: state changed after planning; rerun recovery")
		return 1
	}
	if err := mergemaint.SaveTracking(tracking, report.Failed); err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: save recovery tracking: %v\n", err)
		return 1
	}
	if err := mergemaint.Cleanup(commandEnv("ROOT", "/"), *vdbDir, *journalDir, report.Failed); err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: clean failed VDB entries: %v\n", err)
		return 1
	}
	if err := lock.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: release VDB lock: %v\n", err)
		return 1
	}
	if err := trackingLock.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: release tracking lock: %v\n", err)
		return 1
	}
	targets := make([]string, 0, len(report.Failed))
	for _, failed := range report.Failed {
		targets = append(targets, failed.Atom)
	}
	cfg := resolveFlagsToConfig(false, false)
	cfg.Reinstall, cfg.Oneshot, cfg.CompleteGraph = true, true, true
	runResolve(targets, *dbPath, *repoPath, cfg)
	if err := mergemaint.PurgeTracking(tracking); err != nil {
		fmt.Fprintf(os.Stderr, "maintain merges: packages rebuilt but recovery tracking remains: %v\n", err)
		return 1
	}
	return 0
}

func sameFailedMerges(left, right mergemaint.Report) bool {
	if len(left.Failed) != len(right.Failed) {
		return false
	}
	for index := range left.Failed {
		a, b := left.Failed[index], right.Failed[index]
		if a.Entry != b.Entry || a.CPV != b.CPV || a.Atom != b.Atom || a.Path != b.Path ||
			a.MTimeUnix != b.MTimeUnix || a.Present != b.Present {
			return false
		}
	}
	return true
}

func printFailedMerges(report mergemaint.Report) {
	if len(report.Failed) == 0 {
		fmt.Fprintln(os.Stdout, "No failed merges found.")
		return
	}
	fmt.Fprintf(os.Stdout, "Found %d failed merge(s):\n", len(report.Failed))
	for _, failed := range report.Failed {
		fmt.Fprintf(os.Stdout, "  %s -> %s\n", failed.Entry, failed.Atom)
	}
}

func countTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}
