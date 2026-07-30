package main

import (
	"fmt"
	"io"
)

type commandHelpEntry struct {
	Usage   string
	Summary string
}

var commandHelp = map[string]commandHelpEntry{
	"sync":              {"arise sync [repository...]", "Synchronize repositories and refresh the resolver index."},
	"index":             {"arise index", "Refresh the resolver metadata index."},
	"install":           {"arise install [options] <atom|set>...", "Resolve and install packages."},
	"update":            {"arise update [options] [atom|set]...", "Update packages; defaults to @world."},
	"uninstall":         {"arise uninstall [options] <exact-cpv>...", "Verify and remove exact installed packages."},
	"select":            {"arise select <installed-atom>", "Add an installed package to the world set."},
	"recover":           {"arise recover <status|rollback|inspect-set|restore-set|verify-set|prune-sets> ...", "Inspect or apply journal and recovery-set operations."},
	"query":             {"arise query <atom>", "Query indexed repository metadata."},
	"state":             {"arise state [atom]", "Inspect installed and repository state."},
	"search":            {"arise search [options] <term>...", "Search indexed packages."},
	"installed":         {"arise installed [pattern]", "List installed packages, optionally filtered by pattern."},
	"info":              {"arise info", "Show system and Arise configuration information."},
	"audit":             {"arise audit <python|perl> [options]", "Audit language package state."},
	"perl-cleaner":      {"arise perl-cleaner <mode> [options]", "Repair packages affected by Perl transitions."},
	"python-cleaner":    {"arise python-cleaner <--check|--pretend|--fix|--resume>", "Plan or apply validated Python recovery."},
	"maintain":          {"arise maintain <world|moveinst|mergestate> ...", "Run installed-state maintenance operations."},
	"bug-report":        {"arise bug-report [options]", "Create a reviewable local diagnostic bundle."},
	"dispatch-conf":     {"arise dispatch-conf [options]", "Review protected configuration updates."},
	"quickpkg":          {"arise quickpkg [--gpkg] <atom>", "Create a binary package from an installed package."},
	"depclean":          {"arise depclean", "Plan removal of orphaned dependencies."},
	"prune":             {"arise prune", "Plan removal of superseded installed package versions."},
	"env-update":        {"arise env-update", "Regenerate the system environment."},
	"ldconfig":          {"arise ldconfig", "Refresh the dynamic linker cache."},
	"config":            {"arise config <installed-atom>", "Run an installed package's pkg_config phase."},
	"news":              {"arise news <list|read|display>", "List, mark, or display repository news."},
	"deselect":          {"arise deselect <atom>", "Remove a package from the world set."},
	"preserved-rebuild": {"arise preserved-rebuild", "Rebuild consumers of preserved libraries."},
	"revdep-rebuild":    {"arise revdep-rebuild", "Find and rebuild broken reverse dependencies."},
	"equery":            {"arise equery <belongs|files|uses|size|check|which> ...", "Inspect package ownership and metadata."},
	"bench":             {"arise bench", "Run resolver performance benchmarks."},
}

var commandOrder = []string{
	"sync", "index", "install", "update", "uninstall", "select", "recover",
	"query", "state", "search", "installed", "info", "audit", "perl-cleaner",
	"python-cleaner", "maintain", "bug-report", "dispatch-conf", "quickpkg",
	"depclean", "prune", "env-update", "ldconfig", "config", "news", "deselect",
	"preserved-rebuild", "revdep-rebuild", "equery", "bench",
}

func knownCommand(command string) bool {
	_, ok := commandHelp[command]
	return ok
}

func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help")
}

func writeCommandHelp(writer io.Writer, command string) bool {
	entry, ok := commandHelp[command]
	if !ok {
		return false
	}
	fmt.Fprintf(writer, "Usage: %s\n\n%s\n", entry.Usage, entry.Summary)
	if command == "equery" {
		fmt.Fprintln(writer, "\nSubcommands:")
		fmt.Fprintln(writer, "  belongs <path>  Show the installed package owning a path.")
		fmt.Fprintln(writer, "  files <atom>    List files owned by an installed package.")
		fmt.Fprintln(writer, "  uses <atom>     Show declared and active USE flags.")
		fmt.Fprintln(writer, "  size <atom>     Show installed size.")
		fmt.Fprintln(writer, "  check <atom>    Verify installed files.")
		fmt.Fprintln(writer, "  which <atom>    Show the selected ebuild path.")
		fmt.Fprintln(writer, "\nUse 'arise installed [pattern]' to list installed packages.")
	}
	return true
}
