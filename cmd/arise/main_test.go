package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/search"
)

func TestProgressFramesAreASCII(t *testing.T) {
	for _, frame := range progressFrames {
		for _, b := range []byte(frame) {
			if b > 0x7f {
				t.Fatalf("progress frame %q is not ASCII", frame)
			}
		}
	}
}

func TestFetchProgressNonTerminalStages(t *testing.T) {
	var output bytes.Buffer
	progress := newFetchProgress(true, &output)
	progress.Report(fetch.Progress{Stage: fetch.ProgressChecking, Artifact: "source.tar", Total: 100})
	progress.Report(fetch.Progress{Stage: fetch.ProgressDownload, Artifact: "source.tar", Source: "https://example/source.tar", Total: 100})
	progress.Report(fetch.Progress{Stage: fetch.ProgressDownload, Artifact: "source.tar", Source: "https://example/source.tar", Downloaded: 100, Total: 100})
	progress.Report(fetch.Progress{Stage: fetch.ProgressVerifying, Artifact: "source.tar", Downloaded: 100, Total: 100})
	progress.Report(fetch.Progress{Stage: fetch.ProgressComplete, Artifact: "source.tar", Downloaded: 100, Total: 100})
	got := output.String()
	for _, text := range []string{"Checking source.tar", "Downloading https://example/source.tar", "Verifying source.tar against Manifest", "Fetched and verified source.tar"} {
		if !strings.Contains(got, text) {
			t.Errorf("fetch progress %q omits %q", got, text)
		}
	}
}

func TestFetchProgressQuietIsSilent(t *testing.T) {
	var output bytes.Buffer
	progress := newFetchProgress(false, &output)
	progress.Report(fetch.Progress{Stage: fetch.ProgressDownload, Artifact: "source.tar", Source: "https://example/source.tar", Downloaded: 50, Total: 100})
	progress.Report(fetch.Progress{Stage: fetch.ProgressComplete, Artifact: "source.tar", Downloaded: 100, Total: 100})
	if output.Len() != 0 {
		t.Fatalf("quiet fetch progress = %q", output.String())
	}
}

func TestVersionFlagRegistered(t *testing.T) {
	f := flag.Lookup("version")
	if f == nil {
		t.Fatal("version flag is not registered")
	}
	if f.Usage != "print version and exit" {
		t.Fatalf("version flag usage = %q", f.Usage)
	}
}

func TestHelpUsesShortOrDoubleDashOptionSpellingsOnly(t *testing.T) {
	fs := flag.NewFlagSet("contract", flag.ContinueOnError)
	fs.Bool("q", false, "quiet")
	fs.Bool("pretend", false, "dry run")
	fs.String("log-level", "info", "logging threshold")
	var output bytes.Buffer
	writeUsage(&output, fs)
	got := output.String()
	for _, want := range []string{"  -q", "  --pretend", "  --log-level"} {
		if !strings.Contains(got, want) {
			t.Errorf("help missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"  -pretend", "  -log-level"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("help advertises forbidden single-dash long option %q:\n%s", forbidden, got)
		}
	}
}

func TestEveryDocumentedCommandHasASelectionRoute(t *testing.T) {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	var output bytes.Buffer
	writeUsage(&output, fs)
	const prefix = "Commands: "
	var documented []string
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, prefix) {
			documented = strings.Split(strings.TrimPrefix(line, prefix), ", ")
			break
		}
	}
	if len(documented) == 0 {
		t.Fatal("help has no documented command list")
	}
	seen := make(map[string]bool, len(documented))
	for _, command := range documented {
		if seen[command] {
			t.Fatalf("help documents command %q more than once", command)
		}
		seen[command] = true
		selected, operands := selectCommand([]string{command, "operand"})
		if selected != command || !reflect.DeepEqual(operands, []string{"operand"}) {
			t.Errorf("documented command %q routes as %q with operands %v", command, selected, operands)
		}
	}
}

func TestOptionSpellingContract(t *testing.T) {
	for _, args := range [][]string{
		{"--pretend"}, {"--log-level=debug"}, {"-p"}, {"-uDN"},
		{"-j4"}, {"-j=4"}, {"-l2.5"}, {"-l=2.5"},
		{"install", "--", "-literal-operand"},
	} {
		if err := validateOptionSpellings(args); err != nil {
			t.Errorf("validateOptionSpellings(%q): %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"-pretend"}, {"-log-level=debug"}, {"-jobs=4"}, {"-load-average", "2"},
		{"-uDpretend"}, {"-jwrong"}, {"-lwrong"},
	} {
		err := validateOptionSpellings(args)
		if err == nil {
			t.Errorf("validateOptionSpellings(%q) accepted forbidden spelling", args)
			continue
		}
		if !strings.Contains(err.Error(), "long options require --") {
			t.Errorf("validateOptionSpellings(%q) error = %q", args, err)
		}
	}
}

func TestAdversarialOptionSpellingValidationNeverPanics(t *testing.T) {
	for _, args := range [][]string{
		nil, {""}, {"-"}, {"--"}, {"---"}, {"-\x00"}, {"--\x00"},
		{"-j999999999999999999999999999999"}, {"-lNaN"}, {"-lInf"},
	} {
		_ = validateOptionSpellings(args)
	}
}

func TestDocumentedAriseOptionsUseDoubleDashForLongNames(t *testing.T) {
	man, err := os.ReadFile(filepath.Join("..", "..", "arise.1"))
	if err != nil {
		t.Fatal(err)
	}
	if matches := regexp.MustCompile(`\\fB\\-([a-z][a-z-]+)`).FindAllStringSubmatch(string(man), -1); len(matches) != 0 {
		t.Fatalf("man page contains single-dash long options: %q", matches)
	}
	for _, path := range []string{
		filepath.Join("..", "..", "README.md"),
		filepath.Join("..", "..", "arise.texi"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "arise -repo-url") {
			t.Errorf("%s documents single-dash --repo-url", path)
		}
	}
}

func TestDevelopmentVersionIsNotEmpty(t *testing.T) {
	if strings.TrimSpace(version) == "" {
		t.Fatal("version must not be empty")
	}
}

func TestEmergeShortAliasesRegistered(t *testing.T) {
	for short, long := range map[string]string{
		"1": "oneshot", "u": "update", "O": "nodeps", "o": "onlydeps", "e": "emptytree",
		"N": "newuse", "D": "deep", "p": "pretend", "a": "ask",
		"q": "quiet", "v": "verbose", "t": "tree", "b": "buildpkg",
		"B": "buildpkgonly", "k": "usepkg", "K": "usepkgonly",
		"f": "fetchonly", "n": "noreplace", "g": "getbinpkg",
		"G": "getbinpkgonly", "j": "jobs", "l": "load-average",
	} {
		if flag.Lookup(short) == nil {
			t.Errorf("emerge short option -%s (for --%s) is not registered", short, long)
		}
		if flag.Lookup(long) == nil {
			t.Errorf("emerge long option --%s is not registered", long)
		}
	}
}

func TestP3CanonicalResolverFlagsRegistered(t *testing.T) {
	// This manifest intentionally uses Portage's canonical spellings. Short or
	// legacy aliases do not satisfy the live-operation compatibility gate.
	for _, name := range []string{
		"pretend", "update", "deep", "newuse", "complete-graph",
		"with-bdeps", "keep-going", "backtrack", "emptytree",
		"changed-use", "changed-deps", "dynamic-deps", "nodeps",
		"onlydeps", "root-deps", "usepkg", "usepkgonly",
		"binpkg-respect-use", "resolver-timeout", "jobs", "load-average",
	} {
		if flag.Lookup(name) == nil {
			t.Errorf("P3 canonical option --%s is not registered", name)
		}
	}
}

func TestBashCompletionUsesCanonicalResolverSpellings(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "misc", "arise-completion.bash"))
	if err != nil {
		t.Fatal(err)
	}
	completion := string(data)
	for _, spelling := range []string{"-e", "--emptytree", "--resolver-timeout", "--backtrack", "--complete-graph"} {
		if !strings.Contains(completion, spelling) {
			t.Errorf("completion missing %s", spelling)
		}
	}
	for _, deprecated := range []string{" -emptytree ", " -backtrack ", " -complete-graph "} {
		if strings.Contains(completion, deprecated) {
			t.Errorf("completion advertises non-canonical spelling %q", strings.TrimSpace(deprecated))
		}
	}
}

func TestEmptyTreeLongAndShortFlagsShareState(t *testing.T) {
	original := *emptytree
	defer func() { *emptytree = original }()
	*emptytree = false
	if err := flag.Lookup("e").Value.Set("true"); err != nil {
		t.Fatal(err)
	}
	if !*emptytree {
		t.Fatal("-e did not enable the resolver empty-tree policy")
	}
	if err := flag.Lookup("emptytree").Value.Set("false"); err != nil {
		t.Fatal(err)
	}
	if *emptytree {
		t.Fatal("--emptytree does not share -e state")
	}
}

func TestNormalizeEmergeArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "cluster before command",
			in:   []string{"arise", "-avp", "install", "net-im/signal-desktop-bin"},
			want: []string{"arise", "-a", "-v", "-p", "install", "net-im/signal-desktop-bin"},
		},
		{
			name: "emerge update cluster without command",
			in:   []string{"arise", "-uDN", "@world"},
			want: []string{"arise", "-u", "-D", "-N", "@world"},
		},
		{
			name: "options after command",
			in:   []string{"arise", "install", "--pretend", "-j4", "net-im/signal-desktop-bin"},
			want: []string{"arise", "--pretend", "-j", "4", "install", "net-im/signal-desktop-bin"},
		},
		{
			name: "long option value after command",
			in:   []string{"arise", "update", "--backtrack", "20", "@world"},
			want: []string{"arise", "--backtrack", "20", "update", "@world"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeEmergeArgs(tt.in)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("normalizeEmergeArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectCommandDefaultsToInstall(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantArgs []string
	}{
		{name: "atom", args: []string{"net-im/signal-desktop-bin"}, wantCmd: "install", wantArgs: []string{"net-im/signal-desktop-bin"}},
		{name: "set", args: []string{"@world"}, wantCmd: "install", wantArgs: []string{"@world"}},
		{name: "multiple targets", args: []string{"sys-apps/portage", "@preserved-rebuild"}, wantCmd: "install", wantArgs: []string{"sys-apps/portage", "@preserved-rebuild"}},
		{name: "explicit install", args: []string{"install", "net-im/signal-desktop-bin"}, wantCmd: "install", wantArgs: []string{"net-im/signal-desktop-bin"}},
		{name: "explicit query", args: []string{"query", "sys-apps/portage"}, wantCmd: "query", wantArgs: []string{"sys-apps/portage"}},
		{name: "recover status", args: []string{"recover", "status"}, wantCmd: "recover", wantArgs: []string{"status"}},
		{name: "dispatch explicit path", args: []string{"dispatch-conf", "/etc/ssh"}, wantCmd: "dispatch-conf", wantArgs: []string{"/etc/ssh"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotArgs := selectCommand(tt.args)
			if gotCmd != tt.wantCmd || strings.Join(gotArgs, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Fatalf("selectCommand(%q) = (%q, %q), want (%q, %q)", tt.args, gotCmd, gotArgs, tt.wantCmd, tt.wantArgs)
			}
		})
	}
}

func TestFormatIntegerUsesKiBThousandsSeparators(t *testing.T) {
	if got := formatInteger(112778); got != "112,778" {
		t.Fatalf("formatInteger = %q", got)
	}
}

func TestIndexPrivilegeError(t *testing.T) {
	if err := indexPrivilegeError(1000, "/var/lib/arise/data"); err == nil {
		t.Fatal("non-root system database should require root")
	}
	if err := indexPrivilegeError(0, "/var/lib/arise/data"); err != nil {
		t.Fatalf("root rejected: %v", err)
	}
	if err := indexPrivilegeError(1000, "/tmp/arise-data"); err != nil {
		t.Fatalf("user-owned database rejected: %v", err)
	}
}

func TestSystemDBPathDoesNotCaptureSimilarPrefix(t *testing.T) {
	if !isSystemDBPath("/var/lib/arise/data") || isSystemDBPath("/var/lib/arise-other/data") {
		t.Fatal("system database path boundary is incorrect")
	}
}

func TestSyncPrivilegeError(t *testing.T) {
	if err := syncPrivilegeError(1000, "/var/db/repos/gentoo"); err == nil {
		t.Fatal("non-root system repository should require root")
	}
	if err := syncPrivilegeError(0, "/var/db/repos/gentoo"); err != nil {
		t.Fatalf("root rejected: %v", err)
	}
	if err := syncPrivilegeError(1000, "/tmp/gentoo"); err != nil {
		t.Fatalf("user-owned repository rejected: %v", err)
	}
}

func TestFormatIndexProgress(t *testing.T) {
	got := formatIndexProgress(12345, 5*time.Second)
	if !strings.Contains(got, "12,345 packages") || !strings.Contains(got, "2,469 pkg/s") {
		t.Fatalf("formatIndexProgress() = %q", got)
	}
}

func TestSearchUpgradeAvailable(t *testing.T) {
	if !searchUpgradeAvailable("140.10.2", "152.0.6") {
		t.Fatal("expected upgrade to be detected")
	}
	if searchUpgradeAvailable("152.0.6", "152.0.6") {
		t.Fatal("equal versions are not an upgrade")
	}
}

func TestInstalledAtoms(t *testing.T) {
	vdb := t.TempDir()
	for _, path := range []string{
		"www-client/firefox-140.10.2",
		"www-client/firefox-128.0",
		"dev-go/go-git-5.19.1",
	} {
		if err := os.MkdirAll(filepath.Join(vdb, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cp, err := installedAtoms(vdb, false)
	if err != nil {
		t.Fatal(err)
	}
	wantCP := []string{"dev-go/go-git", "www-client/firefox"}
	if strings.Join(cp, " ") != strings.Join(wantCP, " ") {
		t.Fatalf("installed CP = %v, want %v", cp, wantCP)
	}
	cpv, err := installedAtoms(vdb, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cpv) != 3 {
		t.Fatalf("installed CPV count = %d, want 3: %v", len(cpv), cpv)
	}
	if err := os.WriteFile(filepath.Join(vdb, "www-client/firefox-140.10.2", "repository"), []byte("gentoo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vdb, "www-client/firefox-140.10.2", "BUILD_TIME"), []byte("1778751269\n"), 0644); err != nil {
		t.Fatal(err)
	}
	records, err := scanInstalled(vdb, "repo")
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, record := range records {
		if installedSelectorMatches(record, "repo") {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("repo selector matched %d records, want 1", matched)
	}
}

func TestRestrictionSuffix(t *testing.T) {
	if got := restrictionSuffix("strip !test? ( test )"); got != "^st" {
		t.Fatalf("restrictionSuffix() = %q, want ^st", got)
	}
	if got := restrictionSuffix(""); got != "" {
		t.Fatalf("empty restriction suffix = %q", got)
	}
}

func snapshotFlags() struct {
	backtrackVal       int
	deep               bool
	completeGraph      bool
	newuse             bool
	oneshot            bool
	nodeps             bool
	onlydeps           bool
	emptytree          bool
	reinstall          bool
	changedUse         bool
	changedDeps        bool
	dynamicDeps        bool
	keepGoing          bool
	fetchOnly          bool
	buildPkgOnly       bool
	buildPkg           bool
	usePkg             bool
	usePkgOnly         bool
	pretend            bool
	ask                bool
	quiet              bool
	verbose            bool
	tree               bool
	resume             bool
	skipFirst          bool
	unorderedDisp      bool
	autoUnmaskW        bool
	jobsVal            int
	loadAverage        float64
	withBdeps          string
	binpkgRespectUse   bool
	ignoreBuiltSlotOps string
	getbinpkg          bool
	getbinpkgOnly      bool
	noreplace          bool
} {
	return struct {
		backtrackVal       int
		deep               bool
		completeGraph      bool
		newuse             bool
		oneshot            bool
		nodeps             bool
		onlydeps           bool
		emptytree          bool
		reinstall          bool
		changedUse         bool
		changedDeps        bool
		dynamicDeps        bool
		keepGoing          bool
		fetchOnly          bool
		buildPkgOnly       bool
		buildPkg           bool
		usePkg             bool
		usePkgOnly         bool
		pretend            bool
		ask                bool
		quiet              bool
		verbose            bool
		tree               bool
		resume             bool
		skipFirst          bool
		unorderedDisp      bool
		autoUnmaskW        bool
		jobsVal            int
		loadAverage        float64
		withBdeps          string
		binpkgRespectUse   bool
		ignoreBuiltSlotOps string
		getbinpkg          bool
		getbinpkgOnly      bool
		noreplace          bool
	}{
		backtrackVal:       *backtrackVal,
		deep:               *deep,
		completeGraph:      *completeGraph,
		newuse:             *newuse,
		oneshot:            *oneshot,
		nodeps:             *nodeps,
		onlydeps:           *onlydeps,
		emptytree:          *emptytree,
		reinstall:          *reinstall,
		changedUse:         *changedUse,
		changedDeps:        *changedDeps,
		dynamicDeps:        *dynamicDeps,
		keepGoing:          *keepGoing,
		fetchOnly:          *fetchOnly,
		buildPkgOnly:       *buildPkgOnly,
		buildPkg:           *buildPkg,
		usePkg:             *usePkg,
		usePkgOnly:         *usePkgOnly,
		pretend:            *pretend,
		ask:                *ask,
		quiet:              *quiet,
		verbose:            *verbose,
		tree:               *tree,
		resume:             *resume,
		skipFirst:          *skipFirst,
		unorderedDisp:      *unorderedDisp,
		autoUnmaskW:        *autoUnmaskW,
		jobsVal:            *jobsVal,
		loadAverage:        *loadAverage,
		withBdeps:          *withBdeps,
		binpkgRespectUse:   *binpkgRespectUse,
		ignoreBuiltSlotOps: *ignoreBuiltSlotOps,
		getbinpkg:          *getbinpkg,
		getbinpkgOnly:      *getbinpkgOnly,
		noreplace:          *noreplace,
	}
}

func restoreFlags(orig struct {
	backtrackVal       int
	deep               bool
	completeGraph      bool
	newuse             bool
	oneshot            bool
	nodeps             bool
	onlydeps           bool
	emptytree          bool
	reinstall          bool
	changedUse         bool
	changedDeps        bool
	dynamicDeps        bool
	keepGoing          bool
	fetchOnly          bool
	buildPkgOnly       bool
	buildPkg           bool
	usePkg             bool
	usePkgOnly         bool
	pretend            bool
	ask                bool
	quiet              bool
	verbose            bool
	tree               bool
	resume             bool
	skipFirst          bool
	unorderedDisp      bool
	autoUnmaskW        bool
	jobsVal            int
	loadAverage        float64
	withBdeps          string
	binpkgRespectUse   bool
	ignoreBuiltSlotOps string
	getbinpkg          bool
	getbinpkgOnly      bool
	noreplace          bool
}) {
	*backtrackVal = orig.backtrackVal
	*deep = orig.deep
	*completeGraph = orig.completeGraph
	*newuse = orig.newuse
	*oneshot = orig.oneshot
	*nodeps = orig.nodeps
	*onlydeps = orig.onlydeps
	*emptytree = orig.emptytree
	*reinstall = orig.reinstall
	*changedUse = orig.changedUse
	*changedDeps = orig.changedDeps
	*dynamicDeps = orig.dynamicDeps
	*keepGoing = orig.keepGoing
	*fetchOnly = orig.fetchOnly
	*buildPkgOnly = orig.buildPkgOnly
	*buildPkg = orig.buildPkg
	*usePkg = orig.usePkg
	*usePkgOnly = orig.usePkgOnly
	*pretend = orig.pretend
	*ask = orig.ask
	*quiet = orig.quiet
	*verbose = orig.verbose
	*tree = orig.tree
	*resume = orig.resume
	*skipFirst = orig.skipFirst
	*unorderedDisp = orig.unorderedDisp
	*autoUnmaskW = orig.autoUnmaskW
	*jobsVal = orig.jobsVal
	*loadAverage = orig.loadAverage
	*withBdeps = orig.withBdeps
	*binpkgRespectUse = orig.binpkgRespectUse
	*ignoreBuiltSlotOps = orig.ignoreBuiltSlotOps
	*getbinpkg = orig.getbinpkg
	*getbinpkgOnly = orig.getbinpkgOnly
	*noreplace = orig.noreplace
}

func resetAllFlags() {
	*backtrackVal = 10
	*deep = false
	*completeGraph = false
	*newuse = false
	*oneshot = false
	*nodeps = false
	*onlydeps = false
	*emptytree = false
	*reinstall = false
	*changedUse = false
	*changedDeps = false
	*dynamicDeps = true
	*keepGoing = false
	*fetchOnly = false
	*buildPkgOnly = false
	*buildPkg = false
	*usePkg = false
	*usePkgOnly = false
	*pretend = false
	*ask = false
	*quiet = false
	*verbose = false
	*tree = false
	*resume = false
	*skipFirst = false
	*unorderedDisp = false
	*autoUnmaskW = false
	*jobsVal = 0
	*loadAverage = 0
	*withBdeps = "auto"
	*binpkgRespectUse = false
	*ignoreBuiltSlotOps = "n"
	*getbinpkg = false
	*getbinpkgOnly = false
	*noreplace = false
}

func TestResolveFlagsToConfig_Defaults(t *testing.T) {
	orig := snapshotFlags()
	defer restoreFlags(orig)
	resetAllFlags()

	cfg := resolveFlagsToConfig(false, false)

	if cfg.Backtrack != 10 {
		t.Errorf("Backtrack = %d, want 10", cfg.Backtrack)
	}
	if cfg.Deep {
		t.Error("Deep should be false by default")
	}
	if cfg.Update {
		t.Error("Update should be false")
	}
	if cfg.WithBdeps != "auto" {
		t.Errorf("WithBdeps = %q, want auto", cfg.WithBdeps)
	}
	if !cfg.WithBdepsAuto {
		t.Error("WithBdepsAuto should be true")
	}
	if cfg.BinpkgDir != "/var/cache/binpkgs" {
		t.Errorf("BinpkgDir = %q", cfg.BinpkgDir)
	}
}

func TestResolveFlagsToConfig_UpdateAndDeep(t *testing.T) {
	orig := snapshotFlags()
	defer restoreFlags(orig)

	resetAllFlags()
	cfg := resolveFlagsToConfig(true, false)
	if !cfg.Update {
		t.Error("Update=true via param")
	}
	if cfg.Deep {
		t.Error("Deep should be false")
	}

	resetAllFlags()
	cfg = resolveFlagsToConfig(false, true)
	if !cfg.Deep {
		t.Error("Deep=true via param")
	}

	resetAllFlags()
	*deep = true
	cfg = resolveFlagsToConfig(false, false)
	if !cfg.Deep {
		t.Error("Deep=true via flag")
	}

	resetAllFlags()
	cfg = resolveFlagsToConfig(true, true)
	if !cfg.Update || !cfg.Deep {
		t.Error("both Update and Deep should be true")
	}
}

func TestResolveFlagsToConfig_AllFlagsSet(t *testing.T) {
	orig := snapshotFlags()
	defer restoreFlags(orig)
	resetAllFlags()

	*backtrackVal = 5
	*completeGraph = true
	*newuse = true
	*oneshot = true
	*nodeps = true
	*onlydeps = true
	*emptytree = true
	*reinstall = true
	*changedUse = true
	*changedDeps = true
	*keepGoing = true
	*fetchOnly = true
	*buildPkgOnly = true
	*buildPkg = true
	*usePkg = true
	*usePkgOnly = true
	*pretend = true
	*ask = true
	*quiet = true
	*verbose = true
	*tree = true
	*resume = true
	*skipFirst = true
	*unorderedDisp = true
	*autoUnmaskW = true
	*jobsVal = 4
	*loadAverage = 2.5
	*withBdeps = "auto"
	*binpkgRespectUse = true
	*ignoreBuiltSlotOps = "y"
	*getbinpkg = true
	*getbinpkgOnly = true
	*noreplace = true

	cfg := resolveFlagsToConfig(true, false)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Backtrack", cfg.Backtrack, 5},
		{"Deep", cfg.Deep, false},
		{"CompleteGraph", cfg.CompleteGraph, true},
		{"NewUse", cfg.NewUse, true},
		{"Update", cfg.Update, true},
		{"Oneshot", cfg.Oneshot, true},
		{"NoDeps", cfg.NoDeps, true},
		{"OnlyDeps", cfg.OnlyDeps, true},
		{"EmptyTree", cfg.EmptyTree, true},
		{"Reinstall", cfg.Reinstall, true},
		{"ChangedUse", cfg.ChangedUse, true},
		{"ChangedDeps", cfg.ChangedDeps, true},
		{"KeepGoing", cfg.KeepGoing, true},
		{"FetchOnly", cfg.FetchOnly, true},
		{"BuildPkgOnly", cfg.BuildPkgOnly, true},
		{"BuildPkg", cfg.BuildPkg, true},
		{"UsePkg", cfg.UsePkg, true},
		{"UsePkgOnly", cfg.UsePkgOnly, true},
		{"Pretend", cfg.Pretend, true},
		{"Ask", cfg.Ask, true},
		{"Quiet", cfg.Quiet, true},
		{"Verbose", cfg.Verbose, true},
		{"Tree", cfg.Tree, true},
		{"Resume", cfg.Resume, true},
		{"SkipFirst", cfg.SkipFirst, true},
		{"UnsortedDisplay", cfg.UnsortedDisplay, true},
		{"AutoUnmaskWrite", cfg.AutoUnmaskWrite, true},
		{"Jobs", cfg.Jobs, 4},
		{"LoadAverage", cfg.LoadAverage, 2.5},
		{"WithBdeps", cfg.WithBdeps, "auto"},
		{"WithBdepsAuto", cfg.WithBdepsAuto, true},
		{"BinpkgRespectUse", cfg.BinpkgRespectUse, true},
		{"IgnoreBuiltSlotOperatorDeps", cfg.IgnoreBuiltSlotOperatorDeps, "y"},
		{"GetBinPkg", cfg.GetBinPkg, true},
		{"GetBinPkgOnly", cfg.GetBinPkgOnly, true},
		{"NoReplace", cfg.NoReplace, true},
		{"BinpkgDir", cfg.BinpkgDir, "/var/cache/binpkgs"},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestParseSortField(t *testing.T) {
	tests := []struct {
		input string
		want  search.SortField
	}{
		{"category", search.SortByCategory},
		{"CATEGORY", search.SortByCategory},
		{"version", search.SortByVersion},
		{"VERSION", search.SortByVersion},
		{"slot", search.SortBySlot},
		{"name", search.SortByPackage},
		{"package", search.SortByPackage},
		{"Package", search.SortByPackage},
		{"unknown", search.SortByCategory},
		{"", search.SortByCategory},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSortField(tt.input)
			if got != tt.want {
				t.Errorf("parseSortField(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestActionLabel(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"install", "install"},
		{"update", "update"},
		{"reinstall", "reinstall"},
		{"uninstall", "uninstall"},
		{"block", "block"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			got := actionLabel(tt.action)
			if got != tt.want {
				t.Errorf("actionLabel(%q) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

func TestFetchOnlyUsesFetchPlanLanguage(t *testing.T) {
	result := &resolve.ResolveResult{
		Install:        []resolve.PkgAction{{Action: "install"}},
		BacktrackLevel: 2,
	}
	if got, want := planHeading(result, true), "Fetch plan (1 package, 0 conflicts, backtrack 2):"; got != want {
		t.Fatalf("heading = %q, want %q", got, want)
	}
	if got := displayedActionLabel("install", true); got != "[fetch]" {
		t.Fatalf("fetch-only action label = %q", got)
	}
	if got := displayedActionLabel("update", false); got != "update" {
		t.Fatalf("ordinary action label = %q", got)
	}
}

func TestColorIcon(t *testing.T) {
	for _, action := range []string{"install", "update", "reinstall", "uninstall", "unknown"} {
		t.Run(action, func(t *testing.T) {
			got := colorIcon(action, action)
			if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
				t.Errorf("colorIcon(%q) = %q, want bracketed output", action, got)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatSize(tt.size)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestVdbPathToAtoms(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{"with version", "/var/db/pkg/sys-apps/portage-3.0.51", 1},
		{"without version", "/var/db/pkg/sys-apps/portage", 0},
		{"nested path (extra dir)", "/var/db/pkg/sys-apps/portage-3.0.51/extra", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vdbPathToAtoms(tt.path)
			if len(got) != tt.want {
				t.Errorf("vdbPathToAtoms(%q) = %d atoms, want %d", tt.path, len(got), tt.want)
			}
			if tt.want > 0 && len(got) > 0 {
				parent := filepath.Base(filepath.Dir(tt.path))
				base := filepath.Base(tt.path)
				expected := "=" + parent + "/" + base
				if got[0] != expected {
					t.Errorf("vdbPathToAtoms(%q) = %q, want %q", tt.path, got[0], expected)
				}
			}
		})
	}
}

func TestBuildRebuildConfig_Defaults(t *testing.T) {
	t.Setenv("MAKEOPTS", "-j9")
	var calls []string
	phaseStart := func(phase string) { calls = append(calls, "start:"+phase) }
	phaseEnd := func(phase string, err error) {
		if err != nil {
			calls = append(calls, "end:"+phase+":err")
		} else {
			calls = append(calls, "end:"+phase)
		}
	}

	cfg := buildRebuildConfig("/var/db/repos/gentoo", 4, phaseStart, phaseEnd)

	if cfg.RepoDir != "/var/db/repos/gentoo" {
		t.Errorf("RepoDir = %q", cfg.RepoDir)
	}
	if cfg.DistfilesDir != "/var/cache/distfiles" {
		t.Errorf("DistfilesDir = %q", cfg.DistfilesDir)
	}
	if cfg.RootDir != "/" {
		t.Errorf("RootDir = %q", cfg.RootDir)
	}
	if cfg.VdbDir != "/var/db/pkg" {
		t.Errorf("VdbDir = %q", cfg.VdbDir)
	}
	if cfg.WorkDirBase != "/var/tmp/arise" {
		t.Errorf("WorkDirBase = %q", cfg.WorkDirBase)
	}
	if cfg.MAKEOPTS != "-j9" {
		t.Errorf("MAKEOPTS = %q, want configured -j9; package --jobs must remain separate", cfg.MAKEOPTS)
	}

	cfg.OnPhaseStart("test")
	cfg.OnPhaseEnd("test", nil)
	if len(calls) != 2 {
		t.Errorf("callbacks not wired: got %v", calls)
	}
}

func TestBuildRebuildConfig_ZeroJobs(t *testing.T) {
	cfg := buildRebuildConfig("/tmp/repo", 0, nil, nil)
	if cfg.MAKEOPTS == "-j0" {
		t.Error("MAKEOPTS should fall back to env when jobs <= 0")
	}
}

func TestBuildRebuildConfigLoadsOverlayMasterChain(t *testing.T) {
	root := t.TempDir()
	master, overlay := filepath.Join(root, "gentoo"), filepath.Join(root, "guru")
	for _, directory := range []string{filepath.Join(master, "eclass"), filepath.Join(overlay, "eclass"), filepath.Join(overlay, "metadata"), filepath.Join(root, "repos.conf")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(overlay, "metadata", "layout.conf"), []byte("masters = gentoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := "[gentoo]\nlocation = " + master + "\n[guru]\nlocation = " + overlay + "\n"
	if err := os.WriteFile(filepath.Join(root, "repos.conf", "repositories.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := *portageConfigRoot
	*portageConfigRoot = root
	t.Cleanup(func() { *portageConfigRoot = previous })
	cfg := buildRebuildConfig(overlay, 1, nil, nil)
	directories, err := portage.EclassLookupDirectories(cfg.Repositories, "guru")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(overlay, "eclass"), filepath.Join(master, "eclass")}
	if !reflect.DeepEqual(directories, want) {
		t.Fatalf("overlay eclass lookup = %v, want %v", directories, want)
	}
}

func TestBuildRebuildConfig_EnvOverrides(t *testing.T) {
	os.Setenv("CFLAGS", "-O2 -pipe")
	os.Setenv("CXXFLAGS", "-O2 -pipe")
	os.Setenv("LDFLAGS", "-Wl,-O1")
	os.Setenv("ARCH", "amd64")
	defer func() {
		os.Unsetenv("CFLAGS")
		os.Unsetenv("CXXFLAGS")
		os.Unsetenv("LDFLAGS")
		os.Unsetenv("ARCH")
	}()

	cfg := buildRebuildConfig("/tmp/repo", 1, nil, nil)

	if cfg.CFLAGS != "-O2 -pipe" {
		t.Errorf("CFLAGS = %q", cfg.CFLAGS)
	}
	if cfg.CXXFLAGS != "-O2 -pipe" {
		t.Errorf("CXXFLAGS = %q", cfg.CXXFLAGS)
	}
	if cfg.LDFLAGS != "-Wl,-O1" {
		t.Errorf("LDFLAGS = %q", cfg.LDFLAGS)
	}
	if cfg.Arch != "amd64" {
		t.Errorf("Arch = %q", cfg.Arch)
	}
}

func TestCommandEnvironmentPathSelectors(t *testing.T) {
	t.Setenv("PORTAGE_CONFIGROOT", "/target")
	t.Setenv("ROOT", "/image")
	t.Setenv("DISTDIR", "/cache/dist")

	if got := commandConfigRoot(); got != "/target/etc/portage" {
		t.Fatalf("commandConfigRoot() = %q", got)
	}
	if got := commandRootPath("/var/db/pkg"); got != "/image/var/db/pkg" {
		t.Fatalf("rooted VDB = %q", got)
	}
	if got := commandEnv("DISTDIR", "/fallback"); got != "/cache/dist" {
		t.Fatalf("DISTDIR = %q", got)
	}
}

func TestCommandEnvironmentHonorsExplicitEmptyValue(t *testing.T) {
	t.Setenv("PORTAGE_BINHOST", "")
	if got := commandEnv("PORTAGE_BINHOST", "https://fallback.invalid"); got != "" {
		t.Fatalf("explicit empty PORTAGE_BINHOST = %q", got)
	}
}

func TestUnsupportedRemovalMessageRequiresPretendAndJournal(t *testing.T) {
	for _, command := range []string{"uninstall", "depclean", "prune"} {
		message := unsupportedRemovalMessage(command)
		if !strings.Contains(message, command+" execution is experimental and unavailable") ||
			!strings.Contains(message, "--pretend") || !strings.Contains(message, "P6 journal") {
			t.Fatalf("unsafe %s diagnostic = %q", command, message)
		}
	}
}

func TestUnsupportedRebuildMessageRequiresPretendAndTransaction(t *testing.T) {
	for _, command := range []string{"preserved-rebuild", "revdep-rebuild", "audit --fix"} {
		message := unsupportedRebuildMessage(command)
		if !strings.Contains(message, command+" execution is experimental and unavailable") ||
			!strings.Contains(message, "--pretend") || !strings.Contains(message, "P4/P6") {
			t.Fatalf("unsafe %s diagnostic = %q", command, message)
		}
	}
}

func TestBuildRebuildConfig_NilCallbacks(t *testing.T) {
	cfg := buildRebuildConfig("/tmp/repo", 2, nil, nil)

	if cfg.OnPhaseStart != nil {
		t.Error("OnPhaseStart should be nil")
	}
	if cfg.OnPhaseEnd != nil {
		t.Error("OnPhaseEnd should be nil")
	}
}

func TestSearchExitCode(t *testing.T) {
	if got := searchExitCode(0); got != 1 {
		t.Fatalf("searchExitCode(0) = %d, want 1", got)
	}
	if got := searchExitCode(1); got != 0 {
		t.Fatalf("searchExitCode(1) = %d, want 0", got)
	}
}
