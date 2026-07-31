package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	runtimeTrace "runtime/trace"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/humansize"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/log"
	"github.com/airencracken/arise/internal/search"
)

// version is replaced by release builds with -ldflags "-X main.version=...".
var version = "0.0.15"
var commandContext = context.Background()

func versionLine() string {
	return "arise " + version
}

func commandEnv(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func commandConfigRoot() string {
	root := commandEnv("PORTAGE_CONFIGROOT", "/")
	return filepath.Join(root, "etc", "portage")
}

func commandRootPath(path string) string {
	return filepath.Join(commandEnv("ROOT", "/"), strings.TrimPrefix(path, "/"))
}

var (
	finalizeRuntimeProfiles = func() {}
	showVersion             = flag.Bool("version", false, "print version and exit")
	dbPath                  = flag.String("db", "/var/lib/arise/data", "database path")
	repoPath                = flag.String("repo", commandEnv("PORTDIR", "/var/db/repos/gentoo"), "repository path")
	repoURL                 = flag.String("repo-url", "", "remote repository URL for sync")

	// Filesystem path configuration
	distfilesDir               = flag.String("distfiles-dir", commandEnv("DISTDIR", "/var/cache/distfiles"), "path to distfiles directory")
	vdbDir                     = flag.String("vdb-dir", commandRootPath("/var/db/pkg"), "path to VDB (var/db/pkg)")
	workDir                    = flag.String("work-dir", filepath.Join(commandEnv("PORTAGE_TMPDIR", "/var/tmp"), "arise"), "path to working directory")
	binpkgDir                  = flag.String("binpkg-dir", commandEnv("PKGDIR", "/var/cache/binpkgs"), "path to binary package directory")
	portageConfigRoot          = flag.String("portage-config-root", commandConfigRoot(), "path to portage configuration directory")
	worldFile                  = flag.String("world-file", commandRootPath("/var/lib/portage/world"), "path to world set file")
	resumeFile                 = flag.String("resume-file", filepath.Join(commandEnv("PORTAGE_TMPDIR", "/var/tmp"), "arise", "resume"), "path to resume state file")
	journalDir                 = flag.String("journal-dir", filepath.Join(commandEnv("PORTAGE_TMPDIR", "/var/tmp"), "arise", "journal"), "path to durable operation journals")
	approvePlanSHA256          = flag.String("approve-plan-sha256", "", "authorize exactly the canonical verified plan with this SHA-256 digest")
	approveRecoveryDriftSHA256 = flag.String("approve-recovery-drift-sha256", "", "authorize exactly the inspected recovery-set drift digest")
	approvePlan                = flag.String("approve-plan", "", "authorize from a saved JSON plan path or name")
	savePlan                   = flag.String("save-plan", "", "save the generated JSON plan to a path or name")
	planDir                    = flag.String("plan-dir", filepath.Join(commandEnv("PORTAGE_TMPDIR", "/var/tmp"), "arise", "plans"), "directory for named saved plans")
	emergeLog                  = flag.String("emerge-log", commandRootPath("/var/log/emerge.log"), "Portage-compatible merge timing log")
	showEstimates              = flag.Bool("show-estimates", false, "show historical per-package and total merge-time estimates")
	preflightOnly              = flag.Bool("preflight-only", false, "validate every planned package without fetching, building, or mutating")

	// Logging
	logLevel = flag.String("log-level", "info", "log level: debug, info, warn, error")

	// Package resolution flags
	updateMode               = flag.Bool("update", false, "-u, update packages to the best available version")
	oneshot                  = flag.Bool("oneshot", false, "-1, install without adding to world set")
	nodeps                   = flag.Bool("nodeps", false, "-O, skip dependency resolution")
	onlydeps                 = flag.Bool("onlydeps", false, "-o, only install dependencies")
	onlydepsWithRdeps        = flag.String("onlydeps-with-rdeps", "", "--onlydeps-with-rdeps=y|n")
	onlydepsWithIDeps        = flag.String("onlydeps-with-ideps", "", "--onlydeps-with-ideps=y|n")
	rootDeps                 = flag.String("root-deps", "", "--root-deps=True|rdeps")
	emptytree                = flag.Bool("emptytree", false, "-e, rebuild entire tree as if empty")
	reinstall                = flag.Bool("reinstall", false, "force reinstall of already-installed packages")
	changedUse               = flag.Bool("changed-use", false, "reinstall when USE flags changed")
	changedDeps              = flag.Bool("changed-deps", false, "reinstall when DEPENDs changed")
	dynamicDeps              = flag.Bool("dynamic-deps", true, "use current ebuild dependencies for installed packages")
	newuse                   = flag.Bool("newuse", false, "-N, rebuild when USE flags changed")
	keepGoing                = flag.Bool("keep-going", false, "continue on errors")
	deep                     = flag.Bool("deep", false, "-D, consider full dependency tree")
	completeGraph            = flag.Bool("complete-graph", false, "rebuild reverse deps when packages change")
	backtrackVal             = flag.Int("backtrack", 20, "--backtrack=INT, max backtrack levels")
	resolverTimeout          = flag.Duration("resolver-timeout", 5*time.Minute, "wall-clock resolver limit (0 disables)")
	jobsVal                  = flag.Int("jobs", 0, "-j, parallel jobs")
	fetchJobs                = flag.Int("fetch-jobs", 8, "number of concurrent source fetch and verification jobs (1 disables parallel work)")
	jobsTmpdirRequireFreeGB  = flag.Int("jobs-tmpdir-require-free-gb", 18, "remaining PORTAGE_TMPDIR capacity in GiB required before starting parallel jobs (0 disables)")
	loadAverage              = flag.Float64("load-average", 0, "--load-average=LOAD")
	pretend                  = flag.Bool("pretend", false, "-p, dry run")
	ask                      = flag.Bool("ask", false, "-a, prompt before proceeding")
	quiet                    = flag.Bool("quiet", false, "-q, minimal output")
	verbose                  = flag.Bool("verbose", false, "-v, verbose output")
	debugOutput              = flag.Bool("debug", false, "show resolver timing stages and decision ledger")
	jsonOutput               = flag.Bool("json", false, "emit a versioned JSON resolution plan")
	includeValidationFixture = flag.Bool("include-validation-fixture", false, "include the frozen independent-validation fixture and plan in JSON output")
	tree                     = flag.Bool("tree", false, "-t, display dependency tree")
	resume                   = flag.Bool("resume", false, "--resume, resume last operation")
	skipFirst                = flag.Bool("skipfirst", false, "--skipfirst, skip first package in resume")
	unorderedDisp            = flag.Bool("unordered-display", false, "--unordered-display, don't sort results")
	autoUnmaskW              = flag.Bool("autounmask-write", false, "--autounmask-write, write package.unmask entries")
	withBdeps                = flag.String("with-bdeps", "auto", "--with-bdeps=y|n|auto")
	buildPkg                 = flag.Bool("buildpkg", false, "-b, build binary packages")
	buildPkgOnly             = flag.Bool("buildpkgonly", false, "-B, only build binary packages")
	usePkg                   = flag.Bool("usepkg", false, "-k, use binary packages")
	usePkgOnly               = flag.Bool("usepkgonly", false, "-K, only use binary packages")
	fetchOnly                = flag.Bool("fetchonly", false, "-f, only fetch sources")
	noreplace                = flag.Bool("noreplace", false, "--noreplace, skip packages with exact same version installed")
	colors                   = flag.String("color", "y", "--color=y|n, enable or disable color output")
	deselectArg              = flag.String("deselect", "", "--deselect, remove atom from world set")
	binpkgRespectUse         = flag.Bool("binpkg-respect-use", false, "--binpkg-respect-use, respect USE flags when searching binary packages")
	ignoreBuiltSlotOps       = flag.String("ignore-built-slot-operator-deps", "n", "--ignore-built-slot-operator-deps=y|n")
	getbinpkg                = flag.Bool("getbinpkg", false, "-g, fetch binary packages from remote binhost")
	getbinpkgOnly            = flag.Bool("getbinpkgonly", false, "-G, only use binary packages from remote binhost")

	searchNameOnly   = flag.Bool("name-only", false, "--name-only, search only package/category names")
	searchDesc       = flag.Bool("desc", false, "--desc, search descriptions")
	searchInstalled  = flag.Bool("search-installed", false, "--installed, only show installed packages")
	searchExact      = flag.Bool("exact", false, "--exact, exact match instead of substring")
	searchRegex      = flag.Bool("regex", false, "--regex, use regex patterns")
	searchCategory   = flag.String("search-category", "", "--category, filter by category")
	searchName       = flag.String("search-name", "", "--name, filter by package name")
	searchSlot       = flag.String("search-slot", "", "--slot, filter by SLOT")
	searchUse        = flag.String("search-use", "", "--use, USE flag filter (+flag, -flag)")
	searchKeywords   = flag.String("search-keywords", "", "--keywords, filter by KEYWORDS")
	searchLicense    = flag.String("search-license", "", "--license, filter by LICENSE")
	searchMaintainer = flag.String("search-maintainer", "", "--maintainer, filter by maintainer email")
	searchOrphaned   = flag.Bool("search-orphaned", false, "--orphaned, packages maintained by maintainer-needed")
	searchStable     = flag.Bool("search-stable", false, "--stable, only stable-keyworded packages")
	searchTesting    = flag.Bool("search-testing", false, "--testing, only testing-keyworded packages")
	searchSort       = flag.String("search-sort", "", "--sort, sort by: category, name, version, slot")
	searchCompact    = flag.Bool("compact", false, "--compact, compact output")

	searchVersions   = flag.Bool("search-versions", false, "--versions, show all versions of matching packages")
	searchFormat     = flag.String("search-format", "", "--format, custom format string")
	searchPrint      = flag.String("search-print", "", "--print, specific fields to print (space-separated)")
	searchJSON       = flag.Bool("search-json", false, "--json, JSON output")
	searchBrief      = flag.Bool("search-brief", false, "--brief, one-line minimal output")
	searchOnlyNames  = flag.Bool("search-only-names", false, "--only-names, just package names")
	searchCountOnly  = flag.Bool("search-count-only", false, "--count-only, just print count")
	searchAnd        = flag.Bool("search-and", false, "--and, combine multiple queries with AND")
	searchNot        = flag.String("search-not", "", "--not, exclude packages matching this")
	searchWorld      = flag.Bool("search-world", false, "--world, only packages in @world")
	searchSystem     = flag.Bool("search-system", false, "--system, only packages in @system")
	searchDependsOn  = flag.String("search-depends-on", "", "--depends-on, packages that depend on this")
	searchRequiredBy = flag.String("search-required-by", "", "--required-by, packages that this depends on")
	searchHasUse     = flag.String("search-has-use", "", "--has-use, packages with this USE flag")
	searchHasVersion = flag.String("search-has-version", "", "--has-version, packages with version matching glob")
	searchCare       = flag.Bool("search-care", false, "--care, show packages needing attention")
	searchOverflow   = flag.Bool("search-overflow", false, "--overflow, only packages with masked keywords")
	searchMasked     = flag.Bool("search-masked", false, "--masked, only masked packages")
	searchDuplicates = flag.Bool("search-duplicates", false, "--duplicates, show multiple versions of same package")
	searchDump       = flag.Bool("search-dump", false, "--dump, dump as eix-compatible format")
)

func init() {
	boolAliases := []struct {
		name string
		ptr  *bool
		use  string
	}{
		{"1", oneshot, "alias for --oneshot"},
		{"u", updateMode, "alias for --update"},
		{"O", nodeps, "alias for --nodeps"},
		{"o", onlydeps, "alias for --onlydeps"},
		{"e", emptytree, "alias for --emptytree"},
		{"N", newuse, "alias for --newuse"},
		{"D", deep, "alias for --deep"},
		{"p", pretend, "alias for --pretend"},
		{"a", ask, "alias for --ask"},
		{"q", quiet, "alias for --quiet"},
		{"v", verbose, "alias for --verbose"},
		{"t", tree, "alias for --tree"},
		{"b", buildPkg, "alias for --buildpkg"},
		{"B", buildPkgOnly, "alias for --buildpkgonly"},
		{"k", usePkg, "alias for --usepkg"},
		{"K", usePkgOnly, "alias for --usepkgonly"},
		{"f", fetchOnly, "alias for --fetchonly"},
		{"n", noreplace, "alias for --noreplace"},
		{"g", getbinpkg, "alias for --getbinpkg"},
		{"G", getbinpkgOnly, "alias for --getbinpkgonly"},
	}
	for _, alias := range boolAliases {
		flag.BoolVar(alias.ptr, alias.name, false, alias.use)
	}
	flag.IntVar(jobsVal, "j", 0, "alias for --jobs")
	flag.Float64Var(loadAverage, "l", 0, "alias for --load-average")
	flag.Usage = func() {
		writeUsage(flag.CommandLine.Output(), flag.CommandLine)
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__phase-query" {
		os.Exit(runPhaseQuery(os.Args[2:]))
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	restoreSignalDefaultsAfterCancellation(ctx, stopSignals)
	commandContext = ctx
	defer stopSignals()
	stopRuntimeProfiles := startRuntimeProfilesFromEnvironment()
	finalizeRuntimeProfiles = stopRuntimeProfiles
	defer stopRuntimeProfiles()
	if err := validateOptionSpellings(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "arise: %v\n", err)
		os.Exit(2)
	}
	os.Args = normalizeEmergeArgs(os.Args)
	flag.Parse()
	stopTrapHandler := startDiagnosticTrapHandler()
	defer stopTrapHandler()
	ingest.WriterVersion = version

	if *showVersion {
		fmt.Printf("arise %s\n", version)
		return
	}

	log.SetLevelString(*logLevel)

	if *colors == "n" || *colors == "no" || *colors == "false" || *colors == "0" {
		color.UseColor = false
	}
	if *jsonOutput {
		color.UseColor = false
	}

	args := flag.Args()
	if len(args) == 0 && *deselectArg == "" {
		writeUsage(os.Stderr, flag.CommandLine)
		os.Exit(1)
	}

	if *deselectArg != "" {
		runDeselect(*deselectArg)
		return
	}

	cmd, cmdArgs := selectCommand(args)
	if isHelpRequest(cmdArgs) {
		if writeCommandHelp(os.Stdout, cmd) {
			return
		}
	}
	if *jsonOutput && cmd == "search" {
		*searchJSON = true
	}
	if *jsonOutput && (cmd == "install" || cmd == "update") && !*pretend {
		fmt.Fprintln(os.Stderr, "--json plan output requires --pretend")
		os.Exit(2)
	}

	switch cmd {
	case "sync":
		runSync(cmdArgs, *dbPath, *repoPath, *repoURL)
	case "index":
		runIndex(*dbPath, *repoPath)
	case "query":
		if code := runQuery(cmdArgs, *dbPath); code != 0 {
			os.Exit(code)
		}
	case "state":
		runState(cmdArgs, *dbPath, *vdbDir)
	case "install":
		runInstall(cmdArgs, *dbPath, *repoPath)
	case "uninstall":
		runUninstall(cmdArgs, *dbPath, *repoPath)
	case "recover":
		runRecover(cmdArgs)
	case "audit":
		runAudit(cmdArgs, *repoPath)
	case "maintain":
		if code := runMaintain(cmdArgs); code != 0 {
			os.Exit(code)
		}
	case "bug-report":
		if code := runBugReport(cmdArgs); code != 0 {
			os.Exit(code)
		}
	case "dispatch-conf":
		if code := runDispatchConf(cmdArgs); code != 0 {
			os.Exit(code)
		}
	case "quickpkg":
		runQuickPkg(cmdArgs)
	case "depclean":
		runDepclean(*dbPath, *repoPath)
	case "prune":
		runPrune(*dbPath, *repoPath)
	case "search":
		if code := runSearch(cmdArgs, *dbPath); code != 0 {
			os.Exit(code)
		}
	case "installed":
		if code := runInstalled(cmdArgs, *dbPath, *vdbDir); code != 0 {
			os.Exit(code)
		}
	case "info":
		if code := runInfoQuery(cmdArgs); code != 0 {
			os.Exit(code)
		}
	case "inspect":
		if code := runInspect(cmdArgs); code != 0 {
			os.Exit(code)
		}
	case "preserved-rebuild":
		runPreservedRebuild()
	case "revdep-rebuild":
		runRevdepRebuild()
	case "env-update":
		runEnvUpdate()
	case "ldconfig":
		runLdConfig()
	case "config":
		runConfig(cmdArgs, *repoPath)
	case "news":
		runNews(cmdArgs)
	case "deselect":
		if len(cmdArgs) == 0 {
			fmt.Fprintf(os.Stderr, "deselect: missing atom argument\n")
			os.Exit(1)
		}
		runDeselect(cmdArgs[0])
	case "select":
		if len(cmdArgs) != 1 {
			fmt.Fprintln(os.Stderr, "select: require exactly one installed atom")
			os.Exit(1)
		}
		runSelect(cmdArgs[0])
	case "bench":
		runBench()
	case "perl-cleaner":
		if code := runPerlCleaner(cmdArgs); code != 0 {
			os.Exit(code)
		}
	case "python-cleaner":
		if code := runPythonCleaner(cmdArgs); code != 0 {
			os.Exit(code)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprintf(os.Stderr, "Usage: arise [flags] <command> [args...]\n")
		os.Exit(1)
	}
}

// Restore the process defaults after the first termination signal cancels the
// command. This keeps the first SIGINT cooperative while ensuring a second one
// terminates immediately instead of being swallowed or encouraging SIGQUIT,
// which makes the Go runtime dump every goroutine.
func restoreSignalDefaultsAfterCancellation(ctx context.Context, stop func()) {
	go func() {
		<-ctx.Done()
		stop()
	}()
}

func writeUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: arise [options] <command> [args...]")
	fmt.Fprintln(w, "Commands:")
	for _, command := range commandOrder {
		fmt.Fprintf(w, "  %-19s %s\n", command, commandHelp[command].Summary)
	}
	fmt.Fprintln(w, "Options:")
	var rendered bytes.Buffer
	original := fs.Output()
	fs.SetOutput(&rendered)
	fs.PrintDefaults()
	fs.SetOutput(original)
	for _, line := range strings.Split(rendered.String(), "\n") {
		if strings.HasPrefix(line, "  -") && !strings.HasPrefix(line, "  --") {
			nameEnd := strings.IndexAny(line[3:], " \t=")
			if nameEnd < 0 {
				nameEnd = len(line) - 3
			}
			if nameEnd > 1 {
				line = "  --" + line[3:]
			}
		}
		fmt.Fprintln(w, line)
	}
}

func validateOptionSpellings(args []string) error {
	boolShort := map[byte]bool{'1': true, 'u': true, 'O': true, 'o': true, 'e': true, 'N': true, 'D': true, 'p': true, 'a': true, 'q': true, 'v': true, 't': true, 'b': true, 'B': true, 'k': true, 'K': true, 'f': true, 'n': true, 'g': true, 'G': true}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if len(arg) <= 2 || arg[0] != '-' || arg[1] == '-' {
			continue
		}
		allShort := true
		for index := 1; index < len(arg); index++ {
			if !boolShort[arg[index]] {
				allShort = false
				break
			}
		}
		if allShort {
			continue
		}
		if arg[1] == 'j' {
			if _, err := strconv.Atoi(strings.TrimPrefix(arg[2:], "=")); err == nil {
				continue
			}
		}
		if arg[1] == 'l' {
			if _, err := strconv.ParseFloat(strings.TrimPrefix(arg[2:], "="), 64); err == nil {
				continue
			}
		}
		name := arg[1:]
		if equals := strings.IndexByte(name, '='); equals >= 0 {
			name = name[:equals]
		}
		return fmt.Errorf("invalid option %q: long options require --%s", arg, name)
	}
	return nil
}

func exitAfterRuntimeProfiles(code int) {
	finalizeRuntimeProfiles()
	os.Exit(code)
}

func startRuntimeProfilesFromEnvironment() func() {
	var cpuProfile, goTrace *os.File
	if path := os.Getenv("ARISE_CPU_PROFILE"); path != "" {
		profile, err := os.Create(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "profile: create CPU profile: %v\n", err)
		} else if err := pprof.StartCPUProfile(profile); err != nil {
			fmt.Fprintf(os.Stderr, "profile: start CPU profile: %v\n", err)
			_ = profile.Close()
		} else {
			cpuProfile = profile
		}
	}
	if path := os.Getenv("ARISE_GO_TRACE"); path != "" {
		traceFile, err := os.Create(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "profile: create Go trace: %v\n", err)
		} else if err := runtimeTrace.Start(traceFile); err != nil {
			fmt.Fprintf(os.Stderr, "profile: start Go trace: %v\n", err)
			_ = traceFile.Close()
		} else {
			goTrace = traceFile
		}
	}
	heapPath, allocsPath := os.Getenv("ARISE_HEAP_PROFILE"), os.Getenv("ARISE_ALLOCS_PROFILE")
	if cpuProfile == nil && goTrace == nil && heapPath == "" && allocsPath == "" {
		return func() {}
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if goTrace != nil {
				runtimeTrace.Stop()
				if err := goTrace.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "profile: close Go trace: %v\n", err)
				}
			}
			if cpuProfile != nil {
				pprof.StopCPUProfile()
			}
			if heapPath != "" {
				writeRuntimeProfile(heapPath, "heap")
			}
			if allocsPath != "" {
				writeRuntimeProfile(allocsPath, "allocs")
			}
			if cpuProfile != nil {
				if err := cpuProfile.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "profile: close CPU profile: %v\n", err)
				}
			}
		})
	}
	return stop
}

func writeRuntimeProfile(path, name string) {
	profile := pprof.Lookup(name)
	if profile == nil {
		fmt.Fprintf(os.Stderr, "profile: runtime profile %q is unavailable\n", name)
		return
	}
	output, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile: create %s profile: %v\n", name, err)
		return
	}
	if err := profile.WriteTo(output, 0); err != nil {
		fmt.Fprintf(os.Stderr, "profile: write %s profile: %v\n", name, err)
	}
	if err := output.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "profile: close %s profile: %v\n", name, err)
	}
}

func selectCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	if knownCommand(args[0]) {
		return args[0], args[1:]
	}
	return "install", args
}

func normalizeEmergeArgs(args []string) []string {
	if len(args) < 2 {
		return args
	}
	expanded := []string{args[0]}
	boolShort := map[byte]bool{'1': true, 'u': true, 'O': true, 'o': true, 'e': true, 'N': true, 'D': true, 'p': true, 'a': true, 'q': true, 'v': true, 't': true, 'b': true, 'B': true, 'k': true, 'K': true, 'f': true, 'n': true, 'g': true, 'G': true}
	for _, arg := range args[1:] {
		if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
			allBool := true
			for i := 1; i < len(arg); i++ {
				if !boolShort[arg[i]] {
					allBool = false
					break
				}
			}
			if allBool {
				for i := 1; i < len(arg); i++ {
					expanded = append(expanded, "-"+string(arg[i]))
				}
				continue
			}
			if (arg[1] == 'j' || arg[1] == 'l') && len(arg) > 2 {
				expanded = append(expanded, "-"+string(arg[1]), arg[2:])
				continue
			}
		}
		expanded = append(expanded, arg)
	}

	commandAt := -1
	commands := map[string]bool{"install": true, "uninstall": true, "search": true}
	for i := 1; i < len(expanded); i++ {
		if commands[expanded[i]] {
			commandAt = i
			break
		}
	}
	if commandAt < 0 {
		return expanded
	}

	prefix := append([]string{}, expanded[:commandAt]...)
	command := expanded[commandAt]
	var operands []string
	for i := commandAt + 1; i < len(expanded); i++ {
		arg := expanded[i]
		if !strings.HasPrefix(arg, "-") {
			operands = append(operands, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		valueSuffix := ""
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			valueSuffix = name[eq:]
			name = name[:eq]
		}
		f := flag.Lookup(name)
		if f == nil && command == "search" {
			if searchFlag := flag.Lookup("search-" + name); searchFlag != nil {
				name = "search-" + name
				arg = "--" + name + valueSuffix
				f = searchFlag
			}
		}
		if f == nil {
			operands = append(operands, arg)
			continue
		}
		prefix = append(prefix, arg)
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bf.IsBoolFlag() {
			if !strings.Contains(arg, "=") && i+1 < len(expanded) {
				i++
				prefix = append(prefix, expanded[i])
			}
		}
	}
	return append(append(prefix, command), operands...)
}

func vdbPathToAtoms(vdbPath string) []string {
	base := filepath.Base(vdbPath)
	parent := filepath.Base(filepath.Dir(vdbPath))
	var atoms []string
	if strings.Contains(base, "-") {
		atoms = append(atoms, "="+parent+"/"+base)
	}
	return atoms
}

func parseSortField(s string) search.SortField {
	switch strings.ToLower(s) {
	case "version":
		return search.SortByVersion
	case "slot":
		return search.SortBySlot
	case "name", "package":
		return search.SortByPackage
	default:
		return search.SortByCategory
	}
}

func actionLabel(action string) string {
	switch action {
	case "install":
		return "install"
	case "update":
		return "update"
	case "reinstall":
		return "reinstall"
	case "uninstall":
		return "uninstall"
	default:
		return action
	}
}

func colorIcon(action, label string) string {
	switch action {
	case "install":
		return "[" + color.Green(label) + "]"
	case "update":
		return "[" + color.Cyan(label) + "]"
	case "reinstall":
		return "[" + color.Yellow(label) + "]"
	case "uninstall":
		return "[" + color.Red(label) + "]"
	default:
		return "[" + label + "]"
	}
}

func formatSize(size int64) string {
	if size < 0 {
		return fmt.Sprintf("%d B", size)
	}
	return humansize.Bytes(uint64(size))
}
