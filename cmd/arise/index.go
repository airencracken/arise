package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/nameindex"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/resolversnapshot"
	"github.com/airencracken/arise/internal/snapshotstore"
	"github.com/airencracken/arise/internal/walker"
	"golang.org/x/term"
)

func runIndex(dbPath, repoPath string) {
	exitIfIndexInterrupted(commandContext)
	if err := indexPrivilegeError(os.Geteuid(), dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "index: %v\n", err)
		os.Exit(1)
	}
	candidate, err := snapshotstore.Prepare(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: prepare snapshot: %v\n", err)
		os.Exit(1)
	}
	if err := candidate.SeedFromActive(); err != nil {
		fmt.Fprintf(os.Stderr, "index: seed snapshot: %v\n", err)
		os.Exit(1)
	}
	writePath := candidate.GenerationPath
	cacheRoots := []string{filepath.Join(repoPath, "metadata", "md5-cache")}
	seenRoots := map[string]bool{cacheRoots[0]: true}
	if repos, readErr := portage.ReadReposConf(filepath.Join(*portageConfigRoot, "repos.conf")); readErr == nil {
		for _, repo := range repos {
			cacheRoot := filepath.Join(repo.Location, "metadata", "md5-cache")
			if repo.Location == "" || seenRoots[cacheRoot] {
				continue
			}
			cacheRoots = append(cacheRoots, cacheRoot)
			seenRoots[cacheRoot] = true
		}
	}
	cacheResults, cacheErrs := walker.WalkCacheRoots(cacheRoots)
	fallbackResults, fallbackErrs := walker.WalkUncachedEbuildRoots(cacheRoots)
	results, errs := walker.MergeWalks(cacheResults, fallbackResults, cacheErrs, fallbackErrs)
	db, err := ingest.OpenDB(writePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: open db: %v\n", err)
		os.Exit(1)
	}
	started := time.Now()
	interactive := term.IsTerminal(int(os.Stdout.Fd()))
	fmt.Printf("%s Gentoo metadata\n", color.Bold("Indexing"))
	fmt.Printf("  Repositories: %d\n", len(cacheRoots))
	if !interactive {
		fmt.Printf("  Repository: %s\n", repoPath)
	}

	var parseErrors int
	errsDone := make(chan struct{})
	go func() {
		defer close(errsDone)
		for e := range errs {
			parseErrors++
			if *verbose {
				fmt.Fprintf(os.Stderr, "index: %v\n", e)
			}
		}
	}()

	lastUpdate := time.Time{}
	stats, seen, err := ingest.ReconcileWithProgress(db, results, func(count int) {
		exitIfIndexInterrupted(commandContext)
		if !interactive || (!lastUpdate.IsZero() && time.Since(lastUpdate) < 100*time.Millisecond) {
			return
		}
		lastUpdate = time.Now()
		fmt.Printf("\r\033[2K  %s", formatIndexProgress(count, time.Since(started)))
	})
	if err != nil {
		if interactive {
			fmt.Println()
		}
		fmt.Fprintf(os.Stderr, "index: ingest: %v\n", err)
		os.Exit(1)
	}
	<-errsDone
	exitIfIndexInterrupted(commandContext)
	if parseErrors == 0 {
		stats.Removed, err = ingest.RemoveMissing(db, seen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "index: remove stale packages: %v\n", err)
			os.Exit(1)
		}
	}
	if interactive {
		fmt.Printf("\r\033[2K")
	}
	var packageNames []string
	if err := ingest.QueryKeys(db, "pkg:", func(cp string) error {
		packageNames = append(packageNames, cp)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "index: collect package names: %v\n", err)
		os.Exit(1)
	}
	if err := nameindex.Write(nameindex.Path(writePath), packageNames); err != nil {
		fmt.Fprintf(os.Stderr, "index: write name index: %v\n", err)
		os.Exit(1)
	}
	if err := resolversnapshot.Write(db, writePath, 0); err != nil {
		fmt.Fprintf(os.Stderr, "index: write resolver snapshot: %v\n", err)
		os.Exit(1)
	}
	exitIfIndexInterrupted(commandContext)
	recordCount, validationErr := ingest.CountRecords(db)
	if validationErr != nil || recordCount != len(seen) {
		fmt.Fprintf(os.Stderr, "index: validate candidate: records=%d expected=%d error=%v\n", recordCount, len(seen), validationErr)
		os.Exit(1)
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "index: close db: %v\n", err)
		os.Exit(1)
	}
	if err := ingest.MakeReadable(writePath); err != nil {
		fmt.Fprintf(os.Stderr, "index: publish readable database: %v\n", err)
		os.Exit(1)
	}
	if isSystemDBPath(dbPath) {
		if err := os.Chmod("/var/lib/arise", 0755); err != nil {
			fmt.Fprintf(os.Stderr, "index: publish database directory: %v\n", err)
			os.Exit(1)
		}
	}
	if err := candidate.Publish(); err != nil {
		fmt.Fprintf(os.Stderr, "index: publish snapshot: %v\n", err)
		os.Exit(1)
	}
	if err := candidate.Prune(2); err != nil {
		fmt.Fprintf(os.Stderr, "index: prune old snapshots: %v\n", err)
	}
	fmt.Printf("%s Scanned %d entries in %s (%d changed, %d unchanged, %d removed)", color.Green("Done."), stats.Seen, time.Since(started).Round(time.Millisecond), stats.Changed, stats.Unchanged, stats.Removed)
	if parseErrors > 0 {
		fmt.Printf(" (%d non-fatal parse errors, use -v to see)", parseErrors)
	}
	fmt.Println()
}

func exitIfIndexInterrupted(ctx context.Context) {
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		return
	}
	fmt.Fprintln(os.Stderr, "index: interrupted by user")
	os.Exit(130)
}

func formatIndexProgress(count int, elapsed time.Duration) string {
	rate := float64(0)
	if elapsed > 0 {
		rate = float64(count) / elapsed.Seconds()
	}
	return fmt.Sprintf("%s packages  •  %s pkg/s  •  %s", formatIndexCount(count), formatIndexCount(int(rate)), elapsed.Round(time.Second))
}

func formatIndexCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func indexPrivilegeError(euid int, dbPath string) error {
	if euid != 0 && isSystemDBPath(dbPath) {
		return fmt.Errorf("writing the system database at %s requires root; run su -c 'arise index' or select a user-owned path with -db", dbPath)
	}
	return nil
}

func isSystemDBPath(dbPath string) bool {
	return dbPath == "/var/lib/arise" || strings.HasPrefix(dbPath, "/var/lib/arise/")
}
