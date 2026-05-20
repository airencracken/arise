package audit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	pythonSitePackagesRE = regexp.MustCompile(`/usr/lib(?:64)?/python(\d+\.\d+)/`)
	perlVendorPathRE      = regexp.MustCompile(`/usr/lib(?:64)?/perl5/(?:(?:vendor|site)_perl/)?(\d+\.\d+(?:\.\d+)?)/`)
)

type VdbAuditResult struct {
	PackagePath      string
	OldVersions      []string
	AffectedContents []string
	AuditType        string // "python" or "perl"
}

type auditScanner struct {
	vdbPath    string
	pathRegexp *regexp.Regexp
	auditType  string
	getSystemVersion func() (string, error)
}

func (s *auditScanner) findVersions(path string) []string {
	matches := s.pathRegexp.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var versions []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			versions = append(versions, m[1])
		}
	}
	sort.Strings(versions)
	return versions
}

func (s *auditScanner) scan() ([]VdbAuditResult, error) {
	entries, err := os.ReadDir(s.vdbPath)
	if err != nil {
		return nil, fmt.Errorf("reading vdb path %q: %w", s.vdbPath, err)
	}

	sysVer, err := s.getSystemVersion()
	noSysVer := err != nil

	var results []VdbAuditResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childPath := filepath.Join(s.vdbPath, entry.Name())

		pkgDir, hasContents := resolvePackageDir(childPath)
		if hasContents {
			r := s.processPackage(pkgDir, entry.Name(), noSysVer, sysVer)
			if r != nil {
				results = append(results, *r)
			}
			continue
		}

		subEntries, rerr := os.ReadDir(childPath)
		if rerr != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			subPkgDir := filepath.Join(childPath, sub.Name())
			fullName := entry.Name() + "/" + sub.Name()
			r := s.processPackage(subPkgDir, fullName, noSysVer, sysVer)
			if r != nil {
				results = append(results, *r)
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].PackagePath < results[j].PackagePath
	})

	return results, nil
}

func (s *auditScanner) processPackage(pkgDir, displayName string, noSysVer bool, sysVer string) *VdbAuditResult {
	contentsPath := filepath.Join(pkgDir, "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var affected []string
	versionSet := make(map[string]bool)
	var versionsByContent []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		filePath := parts[1]

		// quick filter before running the regexp
		switch s.auditType {
		case "python":
			if !strings.Contains(filePath, "/python") {
				continue
			}
		case "perl":
			if !strings.Contains(filePath, "/perl5") {
				continue
			}
		}

		versions := s.findVersions(filePath)
		if len(versions) == 0 {
			continue
		}

		affected = append(affected, filePath)
		for _, v := range versions {
			if !versionSet[v] {
				versionSet[v] = true
				versionsByContent = append(versionsByContent, v)
			}
		}
	}

	if len(affected) == 0 {
		return nil
	}

	var oldVersions []string
	if noSysVer {
		oldVersions = versionsByContent
	} else {
		for _, v := range versionsByContent {
			if compareVersions(v, sysVer) < 0 {
				oldVersions = append(oldVersions, v)
			}
		}
	}

	return &VdbAuditResult{
		PackagePath:      pkgDir,
		OldVersions:      oldVersions,
		AffectedContents: affected,
		AuditType:        s.auditType,
	}
}

func resolvePackageDir(dir string) (string, bool) {
	contentsPath := filepath.Join(dir, "CONTENTS")
	_, err := os.Stat(contentsPath)
	return dir, err == nil
}

func FindPythonVersionsInPath(path string) []string {
	matches := pythonSitePackagesRE.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var versions []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			versions = append(versions, m[1])
		}
	}
	sort.Strings(versions)
	return versions
}

func AuditPython(vdbPath string) ([]VdbAuditResult, error) {
	scanner := &auditScanner{
		vdbPath:          vdbPath,
		pathRegexp:       pythonSitePackagesRE,
		auditType:        "python",
		getSystemVersion: detectSystemPythonVersion,
	}
	return scanner.scan()
}

func FindPerlVersionsInPath(path string) []string {
	matches := perlVendorPathRE.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var versions []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			versions = append(versions, m[1])
		}
	}
	sort.Strings(versions)
	return versions
}

func AuditPerl(vdbPath string) ([]VdbAuditResult, error) {
	scanner := &auditScanner{
		vdbPath:          vdbPath,
		pathRegexp:       perlVendorPathRE,
		auditType:        "perl",
		getSystemVersion: detectSystemPerlVersion,
	}
	return scanner.scan()
}

func detectSystemPythonVersion() (string, error) {
	cmd := exec.Command("python3", "-c",
		"import sys; sys.stdout.write(f'{sys.version_info.major}.{sys.version_info.minor}')")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" {
		return "", fmt.Errorf("empty python version output")
	}
	return ver, nil
}

func detectSystemPerlVersion() (string, error) {
	cmd := exec.Command("perl", "-e",
		"printf '%d.%d.%d', $^V->{version}[0], $^V->{version}[1], $^V->{version}[2]")
	out, err := cmd.Output()
	if err != nil {
		// fall back to $] which gives something like 5.038002
		cmd2 := exec.Command("perl", "-e", "print $]")
		out2, err2 := cmd2.Output()
		if err2 != nil {
			return "", err
		}
		return parsePerlRevision(string(out2))
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" {
		return "", fmt.Errorf("empty perl version output")
	}
	return ver, nil
}

func parsePerlRevision(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("parsing perl revision %q: expected at least major.minor", raw)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("parsing perl revision %q: %w", raw, err)
	}
	sub, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("parsing perl revision %q: %w", raw, err)
	}
	patch := sub % 1000
	minor := sub / 1000
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func compareVersions(a, b string) int {
	ap := parseVersion(a)
	bp := parseVersion(b)
	if len(ap) == 0 || len(bp) == 0 {
		return 0
	}
	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}
	for i := 0; i < maxLen; i++ {
		an := 0
		bn := 0
		if i < len(ap) {
			an = ap[i]
		}
		if i < len(bp) {
			bn = bp[i]
		}
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums = append(nums, n)
	}
	return nums
}
