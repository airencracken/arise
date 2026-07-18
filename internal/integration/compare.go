package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/eclass"
	"github.com/airencracken/arise/internal/metadata"
	ariseportage "github.com/airencracken/arise/internal/portage"
)

func liveCommand(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	deadline := 30 * time.Second
	switch name {
	case "portageq":
		deadline = 10 * time.Second
	case "equery":
		deadline = 45 * time.Second
	case "emerge":
		deadline = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, name, args...)
	if name == "emerge" {
		cmd.Env = withoutNews(os.Environ())
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}

func withoutNews(environment []string) []string {
	result := append([]string(nil), environment...)
	for i, entry := range result {
		if strings.HasPrefix(entry, "FEATURES=") {
			result[i] = entry + " -news"
			return result
		}
	}
	return append(result, "FEATURES=-news")
}

// atomTestCases are pairs of version strings used to compare arise's version
// comparison against portage's vercmp.
var atomTestCases = []struct{ a, b string }{
	{"1.0", "1.0"},
	{"1.0", "2.0"},
	{"2.0", "1.0"},
	{"1.2.3", "1.2.3-r1"},
	{"1.2.3-r1", "1.2.3"},
	{"1.2.3a", "1.2.3"},
	{"1.2.3", "1.2.3a"},
	{"1.2.3_alpha", "1.2.3"},
	{"1.2.3", "1.2.3_alpha"},
	{"1.2.3_beta1", "1.2.3_beta2"},
	{"1.2.3_rc1", "1.2.3_rc2"},
	{"1.2.3_p1", "1.2.3_p2"},
	{"1.2.3_pre1", "1.2.3_pre2"},
	{"1.2.3_alpha", "1.2.3_beta"},
	{"1.2.3_beta", "1.2.3_pre"},
	{"1.2.3_pre", "1.2.3_rc"},
	{"1.2.3_rc", "1.2.3"},
	{"1.2.3", "1.2.3_p"},
	{"1.2.3_alpha", "1.2.3_p"},
	{"1.2.3_alpha1", "1.2.3_alpha2"},
	{"1.0", "1.0.0"},
	{"1.0.0", "1.0"},
	{"1.2_rc1", "1.2"},
	{"5.15.0", "5.15.0-r1"},
	{"6.1.0_alpha1", "6.1.0_alpha2"},
	{"3.0.51", "3.0.51-r1"},
}

// portageMetadataFields are the fields to compare between arise and portageq metadata output.
var portageMetadataFields = []string{
	"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND",
	"SLOT", "SRC_URI", "RESTRICT", "PROPERTIES",
	"KEYWORDS", "IUSE", "LICENSE", "REQUIRED_USE",
	"EAPI", "DEFINED_PHASES", "DESCRIPTION", "INHERITED",
}

func runPortageq(t *testing.T, args ...string) string {
	return strings.TrimSpace(runPortageqRaw(t, args...))
}

func runPortageqRaw(t *testing.T, args ...string) string {
	t.Helper()
	cmd := liveCommand(t, "portageq", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("portageq %s: %v (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func runPython(t *testing.T, script string) string {
	t.Helper()
	cmd := liveCommand(t, "python", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("python -c %q: %v (stderr: %s)", script, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

func runEmergeInfo(t *testing.T) string {
	t.Helper()
	cmd := liveCommand(t, "emerge", "--info")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("emerge --info: %v (stderr: %s)", err, stderr.String())
	}
	return stdout.String()
}

func runPortageqAtomMatch(t *testing.T, atomStr string) bool {
	t.Helper()
	cmd := liveCommand(t, "portageq", "match", "/", atomStr)
	err := cmd.Run()
	return err == nil
}

func runPortageqBestVersion(t *testing.T, atomStr string) string {
	t.Helper()
	cmd := liveCommand(t, "portageq", "best_visible", "/", atomStr)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func runPortageqMetadata(t *testing.T, cpv string) string {
	t.Helper()
	args := []string{"metadata", "/", "ebuild", cpv}
	args = append(args, portageMetadataFields...)
	values := strings.Split(strings.TrimSuffix(runPortageqRaw(t, args...), "\n"), "\n")
	var result strings.Builder
	for i, key := range portageMetadataFields {
		if i >= len(values) {
			break
		}
		fmt.Fprintf(&result, "%s=%s\n", key, values[i])
	}
	return result.String()
}

func parseUseFromEmergeInfo(t *testing.T, info string) map[string]bool {
	t.Helper()
	use := make(map[string]bool)
	re := regexp.MustCompile(`^USE="(.*)"$`)
	scanner := regexp.MustCompile(`\n`).Split(info, -1)
	for _, line := range scanner {
		m := re.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 2 {
			for _, f := range strings.Fields(m[1]) {
				if strings.HasPrefix(f, "-") {
					use[f[1:]] = false
				} else {
					use[f] = true
				}
			}
			break
		}
	}
	return use
}

func normalizeMetadataValue(v string) string {
	v = strings.TrimSpace(v)
	v = regexp.MustCompile(`\s+`).ReplaceAllString(v, " ")
	return v
}

func CompareAtoms(t *testing.T) {
	RequirePortage(t)

	atomStrings := []string{
		"sys-devel/gcc",
		">=sys-devel/gcc-12",
		"=sys-devel/gcc-12.2.0",
		"<sys-devel/gcc-13",
		"~sys-devel/gcc-12.2.0",
		"sys-devel/gcc:12",
		"sys-devel/gcc:12/12.2",
		"sys-devel/gcc:=",
		"sys-devel/gcc:*",
		"sys-devel/gcc-12.2.0:12",
		"sys-devel/gcc-12.2.0::gentoo",
		">=sys-devel/gcc-12.2.0:12",
		"=sys-devel/gcc-12.2.0*",
		"sys-devel/gcc[openmp]",
		"sys-devel/gcc[-openmp]",
		"sys-devel/gcc[openmp,pch]",
	}

	for _, raw := range atomStrings {
		t.Run(raw, func(t *testing.T) {
			a, err := atom.Parse(raw)
			if err != nil {
				t.Errorf("arise atom.Parse(%q): %v", raw, err)
				return
			}

			if a.Category == "" || a.Package == "" {
				t.Errorf("arise atom.Parse(%q): empty category or package", raw)
				return
			}

			cp := a.CP()
			valid := runPortageqAtomMatch(t, cp)
			if !valid {
				t.Logf("portageq match for %q returned non-zero (may be expected for some atoms)", cp)
			}

			reparsed := a.String()
			a2, err := atom.Parse(reparsed)
			if err != nil {
				t.Errorf("arise roundtrip parse %q -> %q -> error: %v", raw, reparsed, err)
				return
			}
			if a.CP() != a2.CP() {
				t.Errorf("arise roundtrip CP mismatch: %q vs %q", a.CP(), a2.CP())
			}
		})
	}
}

func CompareMetadata(t *testing.T) {
	RequirePortage(t)

	candidates := []string{
		"sys-devel/gcc",
		"sys-apps/portage",
		"dev-lang/python",
		"sys-libs/glibc",
		"dev-libs/openssl",
	}

	for _, cp := range candidates {
		best := runPortageqBestVersion(t, cp)
		if best == "" {
			t.Logf("no best version for %q, skipping", cp)
			continue
		}

		t.Run(cp, func(t *testing.T) {
			catPkg := cp[:strings.IndexByte(cp, '/')+1] + strings.TrimPrefix(best, "=:")
			_ = catPkg

			portageOutput := runPortageqMetadata(t, best)

			md5cachePath := filepath.Join("/var/db/repos/gentoo/metadata/md5-cache", best)
			data, err := os.ReadFile(md5cachePath)
			if err != nil {
				// try flat layout (no category subdirectory nesting)
				cvParts := strings.SplitN(best, "/", 2)
				if len(cvParts) == 2 {
					flatPath := filepath.Join("/var/db/repos/gentoo/metadata/md5-cache", cvParts[1])
					data, err = os.ReadFile(flatPath)
				}
				if err != nil {
					t.Skipf("md5-cache entry not found for %s: %v", best, err)
					return
				}
			}

			gmMeta, err := metadata.ParseCacheEntry(best, data)
			if err != nil {
				t.Fatalf("arise ParseCacheEntry(%s): %v", best, err)
			}

			portageParsed := parsePortageMetadata(portageOutput)
			compareMetadataFields(t, gmMeta, portageParsed)
		})
	}
}

func parsePortageMetadata(output string) map[string]string {
	parsed := make(map[string]string)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if idx := strings.IndexByte(line, '='); idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			parsed[key] = value
		}
	}
	return parsed
}

func compareMetadataFields(t *testing.T, gmMeta *metadata.PackageMetadata, portageParsed map[string]string) {
	t.Helper()

	gmFields := map[string]string{
		"DEPEND":         gmMeta.DEPEND,
		"RDEPEND":        gmMeta.RDEPEND,
		"BDEPEND":        gmMeta.BDEPEND,
		"IDEPEND":        gmMeta.IDEPEND,
		"PDEPEND":        gmMeta.PDEPEND,
		"SRC_URI":        gmMeta.SRC_URI,
		"RESTRICT":       gmMeta.RESTRICT,
		"PROPERTIES":     gmMeta.PROPERTIES,
		"SLOT":           gmMeta.SLOT,
		"KEYWORDS":       gmMeta.KEYWORDS,
		"IUSE":           gmMeta.IUSE,
		"LICENSE":        gmMeta.LICENSE,
		"REQUIRED_USE":   gmMeta.REQUIRED_USE,
		"EAPI":           gmMeta.EAPI,
		"DEFINED_PHASES": gmMeta.DEFINED_PHASES,
		"DESCRIPTION":    gmMeta.DESCRIPTION,
		"INHERITED":      gmMeta.INHERITED,
	}

	for _, field := range portageMetadataFields {
		ariseV := normalizeMetadataValue(gmFields[field])
		pV := normalizeMetadataValue(portageParsed[field])

		if field == "SLOT" && gmMeta.Subslot != "" {
			if pV == gmMeta.SLOT+"/"+gmMeta.Subslot {
				continue
			}
		}

		if ariseV == "" && pV == "" {
			continue
		}

		if ariseV != pV {
			t.Errorf("field %s mismatch:\n  arise:      %s\n  portage: %s", field, ariseV, pV)
		}
	}
}

func CompareVdbEntries(t *testing.T) {
	RequirePortage(t)

	vdbDir := "/var/db/pkg"
	entries, err := os.ReadDir(vdbDir)
	if err != nil {
		t.Skipf("cannot read /var/db/pkg: %v", err)
		return
	}

	tested := 0
	for _, catEntry := range entries {
		if !catEntry.IsDir() {
			continue
		}
		catDir := filepath.Join(vdbDir, catEntry.Name())
		pkgEntries, err := os.ReadDir(catDir)
		if err != nil {
			continue
		}
		for _, pkgEntry := range pkgEntries {
			if !pkgEntry.IsDir() {
				continue
			}
			cpv := catEntry.Name() + "/" + pkgEntry.Name()
			cpv = strings.TrimPrefix(cpv, "/")

			// Verify portageq knows about this package
			portageCPV := runPortageq(t, "best_version", "/", cpv)
			_ = portageCPV

			// Read the COUNTER or repository file to verify parsing
			pkgDir := filepath.Join(catDir, pkgEntry.Name())
			counterPath := filepath.Join(pkgDir, "COUNTER")
			if counterData, err := os.ReadFile(counterPath); err == nil {
				_ = strings.TrimSpace(string(counterData))
			}

			tested++
			if tested >= 5 {
				break
			}
		}
		if tested >= 5 {
			break
		}
	}

	if tested == 0 {
		t.Log("no VDB entries found (expected in a non-install environment)")
	}
}

func CompareEbuildParsing(t *testing.T) {
	RequirePortage(t)

	best := runPortageqBestVersion(t, "sys-devel/gcc")
	if best == "" {
		best = runPortageqBestVersion(t, "sys-apps/portage")
	}
	if best == "" {
		best = runPortageqBestVersion(t, "dev-libs/openssl")
	}
	if best == "" {
		t.Skip("could not find any best version to test ebuild parsing against")
		return
	}

	category, pkg, version, err := metadata.ParseCPV(best)
	if err != nil {
		t.Fatalf("parse selected CPV %s: %v", best, err)
	}
	repositoryPath := runPortageq(t, "get_repo_path", "/", "gentoo")
	ebuildPath := filepath.Join(repositoryPath, category, pkg, pkg+"-"+version+".ebuild")
	if _, err := os.Stat(ebuildPath); err != nil {
		t.Skipf("could not find ebuild path for %s: %v", best, err)
		return
	}

	e, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatalf("arise ebuild.ParseEbuild(%s): %v", ebuildPath, err)
	}

	portageOutput := runPortageqMetadata(t, best)
	portageParsed := parsePortageMetadata(portageOutput)

	if e.EAPI != portageParsed["EAPI"] {
		t.Errorf("EAPI mismatch: arise=%q portage=%q", e.EAPI, portageParsed["EAPI"])
	}

	portageInherited := splitSortDedup(portageParsed["INHERITED"])
	resolvedInherit, err := eclass.ResolveInheritWithVariables(e.Inherit, repositoryPath, e.Variables)
	if err != nil {
		t.Fatalf("resolve inherited eclasses for %s: %v", best, err)
	}
	gmInherited := sortedDedup(resolvedInherit)

	// portage metadata Inherited is space-separated, arise collects inherit lines
	if len(portageInherited) > 0 || len(gmInherited) > 0 {
		portageSet := strings.Join(portageInherited, " ")
		gmSet := strings.Join(gmInherited, " ")
		if portageSet != gmSet {
			t.Errorf("INHERITED mismatch: arise=%q portage=%q", gmSet, portageSet)
		}
	}

	if len(e.Variables) > 0 {
		t.Logf("arise parsed %d variables from %s", len(e.Variables), filepath.Base(ebuildPath))
	}
}

func splitSortDedup(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	return sortedDedup(parts)
}

func sortedDedup(parts []string) []string {
	if len(parts) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var result []string
	for _, p := range parts {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	sort.Strings(result)
	return result
}

func CompareVersionComparison(t *testing.T) {
	RequirePortage(t)

	for _, tc := range atomTestCases {
		t.Run(fmt.Sprintf("%s_vs_%s", tc.a, tc.b), func(t *testing.T) {
			// Get portage's result via python
			script := fmt.Sprintf(
				"import portage.versions; print(portage.versions.vercmp('%s','%s'))",
				tc.a, tc.b,
			)
			portageResult := runPython(t, script)

			// Get arise's result
			va, err := atom.ParseVersion(tc.a)
			if err != nil {
				t.Fatalf("arise parse version %q: %v", tc.a, err)
			}
			vb, err := atom.ParseVersion(tc.b)
			if err != nil {
				t.Fatalf("arise parse version %q: %v", tc.b, err)
			}
			ariseResult := va.Compare(vb)

			expected := 0
			switch portageResult {
			case "0":
				expected = 0
			case "1":
				expected = 1
			case "-1":
				expected = -1
			default:
				t.Fatalf("unexpected portage vercmp result: %q", portageResult)
			}

			if ariseResult != expected {
				t.Errorf("vercmp(%s, %s): arise=%d portage=%d", tc.a, tc.b, ariseResult, expected)
			}
		})
	}
}

func CompareDepSatisfaction(t *testing.T) {
	RequirePortage(t)

	vdbDir := "/var/db/pkg"
	categories, err := os.ReadDir(vdbDir)
	if err != nil {
		t.Skipf("cannot read /var/db/pkg: %v", err)
		return
	}

	installed := make(map[string]*atom.Atom)
	for _, catEntry := range categories {
		if !catEntry.IsDir() {
			continue
		}
		catDir := filepath.Join(vdbDir, catEntry.Name())
		pkgEntries, err := os.ReadDir(catDir)
		if err != nil {
			continue
		}
		for _, pkgEntry := range pkgEntries {
			if !pkgEntry.IsDir() {
				continue
			}
			cpv := catEntry.Name() + "/" + pkgEntry.Name()
			a, err := atom.Parse(cpv)
			if err != nil {
				continue
			}
			installed[a.CP()] = a
		}
	}

	if len(installed) == 0 {
		t.Skip("no installed packages found in /var/db/pkg")
		return
	}

	// Pick a package that has RDEPEND and test satisfaction
	t.Run("satisfy_simple", func(t *testing.T) {
		depStr := "sys-libs/glibc"
		tree, err := depstring.Parse(depStr)
		if err != nil {
			t.Fatalf("depstring.Parse(%q): %v", depStr, err)
		}

		sat, missing := depstring.Satisfy(tree, installed, nil)
		if !sat {
			t.Logf("dependency %q not satisfied, missing: %v", depStr, missing)
		} else {
			t.Logf("dependency %q satisfied", depStr)
		}
	})

	t.Run("satisfy_with_version", func(t *testing.T) {
		depStr := ">=sys-libs/glibc-2.0"
		tree, err := depstring.Parse(depStr)
		if err != nil {
			t.Fatalf("depstring.Parse(%q): %v", depStr, err)
		}

		sat, missing := depstring.Satisfy(tree, installed, nil)
		if sat {
			t.Logf("dependency %q satisfied", depStr)
		} else {
			t.Logf("dependency %q not satisfied, missing: %v", depStr, missing)
		}
	})

	// Parse RDEPEND from an installed package and test
	t.Run("satisfy_rdepend", func(t *testing.T) {
		for cp, _ := range installed {
			catPkg := cp
			pv := ""
			if idx := strings.LastIndex(catPkg, "-"); idx > strings.IndexByte(catPkg, '/') {
				pv = catPkg[idx+1:]
				catPkg = catPkg[:idx]
			}
			_ = pv

			md5Path := filepath.Join("/var/db/repos/gentoo/metadata/md5-cache", catPkg+"-"+pv)
			data, err := os.ReadFile(md5Path)
			if err != nil {
				continue
			}

			meta, err := metadata.ParseCacheEntry(catPkg+"-"+pv, data)
			if err != nil {
				continue
			}

			if meta.RDEPEND == "" {
				continue
			}

			tree, err := depstring.Parse(meta.RDEPEND)
			if err != nil {
				t.Logf("depstring.Parse RDEPEND for %s: %v", catPkg, err)
				continue
			}

			sat, missing := depstring.Satisfy(tree, installed, nil)
			if !sat {
				t.Logf("%s RDEPEND not fully satisfied, missing: %v", catPkg, missing)
			} else {
				t.Logf("%s RDEPEND satisfied", catPkg)
			}
			break
		}
	})
}

func CompareUseFlags(t *testing.T) {
	RequirePortage(t)

	info := runEmergeInfo(t)
	useFromEmerge := parseUseFromEmergeInfo(t, info)

	if len(useFromEmerge) == 0 {
		t.Skip("could not parse USE flags from emerge --info")
		return
	}

	t.Logf("parsed %d USE flags from emerge --info", len(useFromEmerge))

	// Test USE flag conditional resolution
	t.Run("use_conditional", func(t *testing.T) {
		depStr := "foo? ( sys-devel/gcc )"
		tree, err := depstring.Parse(depStr)
		if err != nil {
			t.Fatalf("depstring.Parse(%q): %v", depStr, err)
		}

		installed := map[string]*atom.Atom{
			"sys-devel/gcc": {Category: "sys-devel", Package: "gcc"},
		}

		satEnabled, _ := depstring.Satisfy(tree, installed, map[string]bool{"foo": true})
		satDisabled, _ := depstring.Satisfy(tree, installed, map[string]bool{"foo": false})

		if !satEnabled {
			t.Error("USE conditional with foo enabled should be satisfied when gcc is installed")
		}
		if !satDisabled {
			t.Error("USE conditional with foo disabled should be satisfied (condition skipped)")
		}
	})

	t.Run("negated_use_conditional", func(t *testing.T) {
		depStr := "!foo? ( sys-devel/gcc )"
		tree, err := depstring.Parse(depStr)
		if err != nil {
			t.Fatalf("depstring.Parse(%q): %v", depStr, err)
		}

		installed := map[string]*atom.Atom{
			"sys-devel/gcc": {Category: "sys-devel", Package: "gcc"},
		}

		satEnabled, _ := depstring.Satisfy(tree, installed, map[string]bool{"foo": true})
		satDisabled, _ := depstring.Satisfy(tree, installed, map[string]bool{"foo": false})

		if !satEnabled {
			t.Error("negated USE conditional with foo enabled should skip the condition")
		}
		if !satDisabled {
			t.Error("negated USE conditional with foo disabled should be satisfied when gcc is installed")
		}
	})
}

type portageUseRecord struct {
	CPV      string `json:"cpv"`
	IUSE     string `json:"iuse"`
	Use      string `json:"use"`
	Keywords string `json:"keywords"`
	Slot     string `json:"slot"`
	Repo     string `json:"repo"`
}

type portageVisibilityCandidate struct {
	CPV      string `json:"cpv"`
	Keywords string `json:"keywords"`
	Slot     string `json:"slot"`
	Repo     string `json:"repo"`
}

type portageVisibilityRecord struct {
	CP         string                       `json:"cp"`
	Best       string                       `json:"best"`
	Candidates []portageVisibilityCandidate `json:"candidates"`
}

func CompareVisibilityCorpus(t *testing.T) {
	RequirePortage(t)
	const script = `
import json, portage
db = portage.db[portage.settings['ROOT']]['porttree'].dbapi
records = []
for cp in sorted(db.cp_all()):
    candidates = []
    for cpv in db.cp_list(cp):
        keywords, slot, repo = db.aux_get(cpv, ['KEYWORDS', 'SLOT', 'repository'])
        candidates.append({'cpv': cpv, 'keywords': keywords, 'slot': slot.split('/', 1)[0], 'repo': repo})
    if len(candidates) < 2:
        continue
    best = db.xmatch('bestmatch-visible', cp)
    records.append({'cp': cp, 'best': best or '', 'candidates': candidates})
    if len(records) == 100:
        break
print(json.dumps(records, sort_keys=True))
`
	var records []portageVisibilityRecord
	if err := json.Unmarshal([]byte(runPython(t, script)), &records); err != nil {
		t.Fatalf("decode Portage visibility corpus: %v", err)
	}
	if len(records) != 100 {
		t.Fatalf("Portage visibility corpus has %d records, want 100", len(records))
	}
	cfg, err := ariseportage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	arch := cfg.MakeConf["ARCH"]
	for _, record := range records {
		best := ""
		var bestVersion *atom.Version
		for _, candidate := range record.Candidates {
			parsed, err := atom.Parse(candidate.CPV)
			if err != nil || parsed.Version == nil {
				t.Fatalf("parse candidate %q: %v", candidate.CPV, err)
			}
			if cfg.PackageMaskStatus(candidate.CPV, candidate.Slot, candidate.Repo).Masked ||
				!cfg.KeywordAcceptedFor(candidate.CPV, candidate.Slot, candidate.Repo, candidate.Keywords, arch) {
				continue
			}
			if bestVersion == nil || parsed.Version.Compare(bestVersion) > 0 {
				best, bestVersion = candidate.CPV, parsed.Version
			}
		}
		if best != record.Best {
			t.Errorf("%s best-visible mismatch: Arise=%q Portage=%q", record.CP, best, record.Best)
		}
	}
}

func CompareEffectivePolicyVariables(t *testing.T) {
	RequirePortage(t)
	cfg, err := ariseportage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	variables := []string{
		"ARCH", "CHOST", "USE_ORDER", "USE_EXPAND", "USE_EXPAND_HIDDEN",
		"USE_EXPAND_IMPLICIT", "ACCEPT_KEYWORDS", "ACCEPT_LICENSE", "FEATURES",
	}
	for _, variable := range variables {
		t.Run(variable, func(t *testing.T) {
			portageValue := normalizeMetadataValue(runPortageq(t, "envvar", variable))
			ariseValue := normalizeMetadataValue(cfg.MakeConf[variable])
			if strings.HasPrefix(variable, "USE_EXPAND") || variable == "FEATURES" {
				portageValue = strings.Join(splitSortDedup(portageValue), " ")
				ariseValue = strings.Join(splitSortDedup(ariseValue), " ")
			}
			if ariseValue != portageValue {
				t.Errorf("%s mismatch: Arise=%q Portage=%q", variable, ariseValue, portageValue)
			}
		})
	}
}

func CompareMaskReasonCorpus(t *testing.T) {
	RequirePortage(t)
	const script = `
import json, portage
db = portage.db[portage.settings['ROOT']]['porttree'].dbapi
records = []
for cp in sorted(db.cp_all()):
    for cpv in db.cp_list(cp):
        reason = portage.getmaskingreason(cpv)
        if not reason:
            continue
        slot, repo = db.aux_get(cpv, ['SLOT', 'repository'])
        records.append({'cpv': cpv, 'slot': slot, 'repo': repo, 'reason': reason})
        if len(records) == 25:
            break
    if len(records) == 25:
        break
print(json.dumps(records, sort_keys=True))
`
	var records []struct{ CPV, Slot, Repo, Reason string }
	if err := json.Unmarshal([]byte(runPython(t, script)), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 25 {
		t.Fatalf("mask reason corpus has %d records", len(records))
	}
	cfg, err := ariseportage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	normalizeReason := func(reason string) string {
		var lines []string
		for _, line := range strings.Split(reason, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
			if line != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, " ")
	}
	for _, record := range records {
		status := cfg.PackageMaskStatus(record.CPV, record.Slot, record.Repo)
		if !status.Masked || status.Reason != normalizeReason(record.Reason) {
			t.Errorf("%s mask reason mismatch: Arise=%q Portage=%q source=%q", record.CPV, status.Reason, normalizeReason(record.Reason), status.Source)
		}
	}
}

func CompareEffectiveUseCorpus(t *testing.T) {
	RequirePortage(t)
	const script = `
import json, portage
db = portage.db[portage.settings['ROOT']]['porttree'].dbapi
records = []
for cp in sorted(db.cp_all()):
    cpv = db.xmatch('bestmatch-visible', cp)
    if not cpv:
        continue
    iuse, keywords, slot, repo = db.aux_get(cpv, ['IUSE', 'KEYWORDS', 'SLOT', 'repository'])
    if not iuse:
        continue
    settings = portage.config(clone=portage.settings)
    settings.setcpv(cpv, mydb=db)
    records.append({'cpv': cpv, 'iuse': iuse, 'use': settings.get('PORTAGE_USE', ''),
                    'keywords': keywords, 'slot': slot.split('/', 1)[0], 'repo': repo})
    if len(records) == 100:
        break
print(json.dumps(records, sort_keys=True))
`
	output := runPython(t, script)
	var records []portageUseRecord
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		t.Fatalf("decode Portage USE corpus: %v", err)
	}
	if len(records) != 100 {
		t.Fatalf("Portage USE corpus has %d records, want 100", len(records))
	}
	cfg, err := ariseportage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	arch := cfg.MakeConf["ARCH"]
	for _, record := range records {
		base := make(map[string]bool)
		for _, raw := range strings.Fields(record.IUSE) {
			base[strings.TrimLeft(raw, "+-")] = strings.HasPrefix(raw, "+")
		}
		stable := false
		for _, keyword := range strings.Fields(record.Keywords) {
			stable = stable || keyword == arch
		}
		for flag, enabled := range cfg.EffectiveUseForStability(record.CPV, record.Slot, record.Repo, stable) {
			if _, declared := base[flag]; declared {
				base[flag] = enabled
			}
		}
		portageEnabled := make(map[string]bool)
		for _, flag := range strings.Fields(record.Use) {
			if _, declared := base[flag]; declared {
				portageEnabled[flag] = true
			}
		}
		var onlyArise, onlyPortage []string
		for flag, enabled := range base {
			if enabled && !portageEnabled[flag] {
				onlyArise = append(onlyArise, flag)
			}
			if !enabled && portageEnabled[flag] {
				onlyPortage = append(onlyPortage, flag)
			}
		}
		sort.Strings(onlyArise)
		sort.Strings(onlyPortage)
		if len(onlyArise) > 0 || len(onlyPortage) > 0 {
			t.Errorf("%s USE mismatch: only Arise=%v only Portage=%v", record.CPV, onlyArise, onlyPortage)
		}
	}
}

// BrokenState represents a detected inconsistency between a system's
// installed state and what emerge reports.
type BrokenState struct {
	Package    string
	Issue      string
	Suggestion string
}

func AnalyzeBrokenState(t *testing.T) []BrokenState {
	RequirePortage(t)

	var states []BrokenState

	vdbDir := "/var/db/pkg"
	categories, err := os.ReadDir(vdbDir)
	if err != nil {
		t.Logf("cannot read /var/db/pkg: %v", err)
		return states
	}

	for _, catEntry := range categories {
		if !catEntry.IsDir() {
			continue
		}
		catDir := filepath.Join(vdbDir, catEntry.Name())
		pkgEntries, err := os.ReadDir(catDir)
		if err != nil {
			continue
		}
		for _, pkgEntry := range pkgEntries {
			if !pkgEntry.IsDir() {
				continue
			}
			cpv := catEntry.Name() + "/" + pkgEntry.Name()

			// Check if this package exists in the Gentoo repository
			best := runPortageqBestVersion(t, cpv)
			if best == "" {
				catSlashPkg := cpv[:strings.LastIndex(cpv, "-")]
				if idx := strings.LastIndex(catSlashPkg, "-"); idx > strings.IndexByte(catSlashPkg, '/') {
					// try again with the category/package
					catPkg := catSlashPkg[:idx]
					best = runPortageqBestVersion(t, catPkg)
				}
			}

			if best == "" {
				states = append(states, BrokenState{
					Package:    cpv,
					Issue:      "not available in gentoo repository",
					Suggestion: "may have been removed from the tree; consider removing with emerge --deselect " + cpv,
				})
				continue
			}

			// Check if a newer version is available
			catPkg := cpv[:strings.LastIndex(cpv, "-")]
			if idx := strings.LastIndex(catPkg, "-"); idx > strings.IndexByte(catPkg, '/') {
				catPkg = catPkg[:idx]
			}
			newestVisible := runPortageqBestVersion(t, catPkg)
			if newestVisible != "" && newestVisible != best && newestVisible != "="+cpv {
				states = append(states, BrokenState{
					Package:    cpv,
					Issue:      fmt.Sprintf("newer version available: %s", newestVisible),
					Suggestion: fmt.Sprintf("emerge -1 %s", catPkg),
				})
			}
		}
	}

	return states
}

func RunAll(t *testing.T) {
	RequirePortage(t)

	t.Run("CompareAtoms", CompareAtoms)
	t.Run("CompareMetadata", CompareMetadata)
	t.Run("CompareVdbEntries", CompareVdbEntries)
	t.Run("CompareEbuildParsing", CompareEbuildParsing)
	t.Run("CompareVersionComparison", CompareVersionComparison)
	t.Run("CompareDepSatisfaction", CompareDepSatisfaction)
	t.Run("CompareUseFlags", CompareUseFlags)
	t.Run("CompareEffectiveUseCorpus", CompareEffectiveUseCorpus)
	t.Run("CompareVisibilityCorpus", CompareVisibilityCorpus)
	t.Run("CompareEffectivePolicyVariables", CompareEffectivePolicyVariables)
	t.Run("CompareMaskReasonCorpus", CompareMaskReasonCorpus)
}
