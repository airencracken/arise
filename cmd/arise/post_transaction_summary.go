package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/news"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/preserved"
)

var configUpdateName = regexp.MustCompile(`^\._cfg[0-9]{4}_.+`)

// printPostTransactionSummary mirrors Portage's useful end-of-run reminders.
// Every collector is read-only and best-effort: a reporting problem must not
// turn successfully committed package transactions into a failed operation.
func printPostTransactionSummary(w io.Writer, root, vdbRoot, repoDir string, cfg *portage.Config) {
	printSavedPackageMessagesSummary(w, root, cfg)
	printPreservedLibrarySummary(w, root, vdbRoot)
	printConfigUpdateSummary(w, root, cfg)
	printUnreadNewsSummary(w, root, repoDir)
}

func printSavedPackageMessagesSummary(w io.Writer, root string, cfg *portage.Config) {
	logDir := filepath.Join(root, "var", "log", "portage")
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.MakeConf["PORTAGE_LOGDIR"]); configured != "" {
			if filepath.IsAbs(configured) {
				logDir = filepath.Join(root, strings.TrimPrefix(filepath.Clean(configured), "/"))
			} else {
				logDir = filepath.Join(root, configured)
			}
		}
	}
	path := filepath.Join(logDir, "elog", "summary.log")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		fmt.Fprintf(w, " * Package messages are saved in %s.\n", displayRootPath(root, path))
		fmt.Fprintf(w, " * Read them later with: less %s\n", displayRootPath(root, path))
	}
}

func printPreservedLibrarySummary(w io.Writer, root, vdbRoot string) {
	libs, err := preserved.ScanPreservedLibs(root)
	if err != nil {
		fmt.Fprintf(w, " * warning: could not inspect preserved libraries: %v\n", err)
		return
	}
	if len(libs) == 0 {
		return
	}
	reasons, reasonErr := preserved.RebuildReasons(root, vdbRoot)
	consumers := make(map[string][]string)
	if reasonErr == nil {
		for _, reason := range reasons {
			if reason.Kind == "preserved-library" {
				consumers[reason.PreservedPath] = appendUniqueString(consumers[reason.PreservedPath], reason.Package)
			}
		}
	}
	fmt.Fprintln(w, "!!! existing preserved libs:")
	for _, lib := range libs {
		owner := lib.OwningPkg
		if owner == "" {
			owner = "unknown owner"
		}
		fmt.Fprintf(w, ">>> package: %s\n *  - %s\n", owner, displayRootPath(root, lib.Path))
		for _, consumer := range consumers[lib.Path] {
			fmt.Fprintf(w, " *      used by %s\n", consumer)
		}
	}
	if reasonErr != nil {
		fmt.Fprintf(w, " * warning: could not inspect preserved-library consumers: %v\n", reasonErr)
	}
	fmt.Fprintln(w, "Use arise @preserved-rebuild to rebuild packages using these libraries")
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	sort.Strings(values)
	return values
}

func displayRootPath(root, path string) string {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	if cleanRoot == "/" {
		return "/" + strings.TrimPrefix(cleanPath, "/")
	}
	if relative, err := filepath.Rel(cleanRoot, cleanPath); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "/" + filepath.ToSlash(relative)
	}
	return cleanPath
}

func pendingConfigUpdates(root string, cfg *portage.Config) ([]string, error) {
	protect := []string{"/etc"}
	mask := []string(nil)
	if cfg != nil {
		if configured := strings.Fields(cfg.MakeConf["CONFIG_PROTECT"]); len(configured) != 0 {
			protect = configured
		}
		mask = strings.Fields(cfg.MakeConf["CONFIG_PROTECT_MASK"])
	}
	seen := make(map[string]bool)
	var pending []string
	for _, configured := range protect {
		base := filepath.Join(root, strings.TrimPrefix(filepath.Clean(configured), "/"))
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if path == base {
					return walkErr
				}
				return nil
			}
			if entry.IsDir() || !configUpdateName.MatchString(entry.Name()) {
				return nil
			}
			display := displayRootPath(root, path)
			for _, excluded := range mask {
				excluded = "/" + strings.Trim(strings.TrimSpace(excluded), "/")
				if display == excluded || strings.HasPrefix(display, excluded+"/") {
					return nil
				}
			}
			if !seen[display] {
				seen[display] = true
				pending = append(pending, display)
			}
			return nil
		})
		if err != nil && !strings.Contains(err.Error(), "no such file or directory") {
			return nil, err
		}
	}
	sort.Strings(pending)
	return pending, nil
}

func printConfigUpdateSummary(w io.Writer, root string, cfg *portage.Config) {
	pending, err := pendingConfigUpdates(root, cfg)
	if err != nil {
		fmt.Fprintf(w, " * warning: could not inspect protected configuration updates: %v\n", err)
		return
	}
	if len(pending) != 0 {
		fmt.Fprintf(w, " * IMPORTANT: %d config files in '/etc' need updating.\n", len(pending))
		fmt.Fprintln(w, " * Run arise dispatch-conf to review protected configuration updates.")
	}
}

func printUnreadNewsSummary(w io.Writer, root, repoDir string) {
	unread, err := news.ReadUnreadNews(filepath.Join(repoDir, "metadata", "news"), filepath.Join(root, "var", "lib", "gentoo", "news"))
	if err != nil {
		fmt.Fprintf(w, " * warning: could not inspect unread news: %v\n", err)
		return
	}
	if len(unread) != 0 {
		fmt.Fprintf(w, " * IMPORTANT: %d news items need reading for repository 'gentoo'.\n", len(unread))
		fmt.Fprintln(w, " * Use arise news display to view them.")
	}
}
