package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/resumemaint"
)

func portageMTimeDBPath() string {
	return commandRootPath("/var/cache/edb/mtimedb")
}

func runMaintainResume(args []string) int {
	check, fix := false, false
	for _, arg := range args {
		switch arg {
		case "--check":
			check = true
		case "--fix":
			fix = true
		case "-h", "--help":
			fmt.Println("Usage: arise [global options] maintain resume --check|--fix")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "maintain resume: unknown option %q\n", arg)
			return 2
		}
	}
	if check == fix {
		fmt.Fprintln(os.Stderr, "maintain resume: require exactly one of --check or --fix")
		return 2
	}
	mtimedb := portageMTimeDBPath()
	report, err := resumemaint.Check(*resumeFile, mtimedb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain resume: %v\n", err)
		return 1
	}
	if check {
		printResumeReport(report)
		if report.HasState() || !report.Valid() {
			return 1
		}
		return 0
	}
	if !report.Valid() {
		printResumeReport(report)
		fmt.Fprintln(os.Stderr, "maintain resume: refusing to remove malformed state")
		return 1
	}
	if !report.HasState() {
		fmt.Fprintln(os.Stdout, "No resume state found.")
		return 0
	}
	if *pretend {
		fmt.Fprintln(os.Stdout, "Resume cleanup plan:")
		if report.Arise.Present {
			fmt.Fprintf(os.Stdout, "  remove %s\n", report.Arise.Path)
		}
		for _, key := range report.PortageKeys {
			fmt.Fprintf(os.Stdout, "  remove Portage mtimedb key %s\n", key)
		}
		return 0
	}
	ariseLock, err := oplock.TryAcquirePath(*resumeFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain resume: acquire Arise resume lock: %v\n", err)
		return 1
	}
	defer ariseLock.Release()
	portageLock, err := oplock.TryAcquirePath(filepath.Join(filepath.Dir(mtimedb), "mtimedb"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain resume: acquire Portage state lock: %v\n", err)
		return 1
	}
	defer portageLock.Release()
	locked, err := resumemaint.Check(*resumeFile, mtimedb)
	if err != nil || !sameResumeReport(report, locked) {
		fmt.Fprintln(os.Stderr, "maintain resume: state changed after planning; rerun cleanup")
		return 1
	}
	if err := resumemaint.Clean(commandEnv("ROOT", "/"), *journalDir, report); err != nil {
		fmt.Fprintf(os.Stderr, "maintain resume: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "Cleared resume state.")
	return 0
}

func printResumeReport(report resumemaint.Report) {
	if !report.Valid() {
		if !report.Arise.Valid {
			fmt.Fprintf(os.Stdout, "Invalid Arise resume state: %s\n", report.Arise.Error)
		}
		if !report.Portage.Valid {
			fmt.Fprintf(os.Stdout, "Invalid Portage resume state: %s\n", report.Portage.Error)
		}
		return
	}
	if !report.HasState() {
		fmt.Fprintln(os.Stdout, "No resume state found.")
		return
	}
	if report.Arise.Present {
		fmt.Fprintf(os.Stdout, "Arise resume state: %d package(s) remaining.\n", report.Arise.Remaining)
	}
	for _, key := range report.PortageKeys {
		fmt.Fprintf(os.Stdout, "Portage mtimedb contains %s.\n", key)
	}
}

func sameResumeReport(left, right resumemaint.Report) bool {
	if left.Arise != right.Arise || left.Portage != right.Portage || len(left.PortageKeys) != len(right.PortageKeys) {
		return false
	}
	for index := range left.PortageKeys {
		if left.PortageKeys[index] != right.PortageKeys[index] {
			return false
		}
	}
	return true
}
