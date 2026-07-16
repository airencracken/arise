package main

import (
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
	"github.com/airencracken/arise/internal/walker"
	"golang.org/x/term"
)

func runIndex(dbPath, repoPath string) {
	if err := indexPrivilegeError(os.Geteuid(), dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "index: %v\n", err)
		os.Exit(1)
	}
	cacheRoots := []string{filepath.Join(repoPath, "metadata", "md5-cache")}
	seenRoots := map[string]bool{cacheRoots[0]: true}
	if repos, readErr := portage.ReadReposConf(filepath.Join(*portageConfigRoot, "repos.conf")); readErr == nil {
		for _, repo := range repos {
			cacheRoot := filepath.Join(repo.Location, "metadata", "md5-cache")
			if repo.Location == "" || seenRoots[cacheRoot] {
				continue
			}
			if info, statErr := os.Stat(cacheRoot); statErr == nil && info.IsDir() {
				cacheRoots = append(cacheRoots, cacheRoot)
				seenRoots[cacheRoot] = true
			}
		}
	}
	results, errs := walker.WalkCacheRoots(cacheRoots)
	db, err := ingest.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
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
	if err := nameindex.Write(nameindex.Path(dbPath), packageNames); err != nil {
		fmt.Fprintf(os.Stderr, "index: write name index: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s Scanned %d entries in %s (%d changed, %d unchanged, %d removed)", color.Green("Done."), stats.Seen, time.Since(started).Round(time.Millisecond), stats.Changed, stats.Unchanged, stats.Removed)
	if parseErrors > 0 {
		fmt.Printf(" (%d non-fatal parse errors, use -v to see)", parseErrors)
	}
	fmt.Println()
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
	if euid != 0 && (dbPath == "/var/lib/arise" || strings.HasPrefix(dbPath, "/var/lib/arise/")) {
		return fmt.Errorf("writing the system database at %s requires root; run su -c 'arise index' or select a user-owned path with -db", dbPath)
	}
	return nil
}
