// Package perlcleaner builds deterministic repair evidence for installed Perl
// modules and libperl consumers without requiring a working Perl interpreter.
package perlcleaner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/vdb"
)

type Mode struct {
	Modules    bool `json:"modules"`
	AllModules bool `json:"all_modules"`
	LibPerl    bool `json:"libperl"`
	Preclean   bool `json:"preclean"`
	Leftovers  bool `json:"leftovers"`
}

func ModulesMode() Mode    { return Mode{Modules: true, Leftovers: true} }
func AllModulesMode() Mode { return Mode{Modules: true, AllModules: true, Leftovers: true} }
func LibPerlMode() Mode    { return Mode{LibPerl: true, Leftovers: true} }
func AllMode() Mode        { return Mode{Modules: true, LibPerl: true, Preclean: true, Leftovers: true} }
func ReallyAllMode() Mode {
	return Mode{Modules: true, AllModules: true, LibPerl: true, Preclean: true, Leftovers: true}
}

func FinalValidationMode(mode Mode) Mode {
	mode.AllModules = false
	mode.Preclean = false
	return mode
}

type ABI struct {
	Version        string   `json:"version"`
	Arch           string   `json:"arch"`
	LibPerlSONames []string `json:"libperl_sonames"`
	SourceCPV      string   `json:"source_cpv"`
}

type Reason struct {
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}

type Action struct {
	CPV     string   `json:"cpv"`
	Atom    string   `json:"atom"`
	Reasons []Reason `json:"reasons"`
}

type Report struct {
	Mode     Mode     `json:"mode"`
	ABI      ABI      `json:"abi"`
	Preclean Preclean `json:"preclean"`
	Actions  []Action `json:"actions"`
}

type Preclean struct {
	PerlCore []string `json:"perl_core"`
	Virtuals []string `json:"virtuals"`
}

var perlVersionSegment = regexp.MustCompile(`(?:^|/)([0-9]+\.[0-9]+)(?:/|$)`)

func Check(vdbRoot string, mode Mode) (Report, error) {
	if !mode.Modules && !mode.LibPerl {
		return Report{}, fmt.Errorf("perl-cleaner: select at least one of modules or libperl")
	}
	packages, err := vdb.Scan(vdbRoot)
	if err != nil {
		return Report{}, err
	}
	abi, err := deriveABI(vdbRoot, packages)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Mode: mode, ABI: abi,
		Preclean: Preclean{PerlCore: []string{}, Virtuals: []string{}},
		Actions:  []Action{},
	}
	for _, pkg := range packages {
		if mode.Preclean {
			switch {
			case pkg.Category == "perl-core":
				report.Preclean.PerlCore = append(report.Preclean.PerlCore, pkg.CP())
			case pkg.Category == "virtual" && strings.HasPrefix(pkg.Package, "perl-"):
				report.Preclean.Virtuals = append(report.Preclean.Virtuals, pkg.CP())
			}
		}
		if excluded(pkg.CP()) {
			continue
		}
		var reasons []Reason
		if mode.Modules {
			modulePaths := perlModulePaths(pkg.Contents)
			for _, path := range modulePaths {
				if mode.AllModules || staleModulePath(path, abi) {
					kind := "stale-module"
					if mode.AllModules {
						kind = "all-modules"
					}
					reasons = append(reasons, Reason{Kind: kind, Evidence: path})
				}
			}
		}
		if mode.LibPerl {
			linked, err := linkedLibPerl(filepath.Join(vdbRoot, pkg.Category, pkg.Package+"-"+pkg.Version, "NEEDED.ELF.2"))
			if err != nil {
				return Report{}, fmt.Errorf("perl-cleaner: inspect %s linkage: %w", pkg.CPV(), err)
			}
			for _, soname := range linked {
				if mode.AllModules || !contains(abi.LibPerlSONames, soname) {
					reasons = append(reasons, Reason{Kind: "libperl", Evidence: soname})
				}
			}
		}
		if len(reasons) == 0 {
			continue
		}
		reasons = uniqueReasons(reasons)
		report.Actions = append(report.Actions, Action{
			CPV: pkg.CPV(), Atom: pkg.CP() + ":" + pkg.Slot, Reasons: reasons,
		})
	}
	sort.Slice(report.Actions, func(i, j int) bool { return report.Actions[i].CPV < report.Actions[j].CPV })
	return report, nil
}

func Targets(report Report) []string {
	targets := append([]string(nil), report.Preclean.Virtuals...)
	for _, action := range report.Actions {
		targets = append(targets, action.Atom)
		if strings.HasPrefix(action.CPV, "perl-core/") {
			name := strings.TrimPrefix(action.Atom, "perl-core/")
			name, _, _ = strings.Cut(name, ":")
			virtual := "virtual/perl-" + name
			if contains(report.Preclean.Virtuals, virtual) {
				targets = append(targets, virtual)
			}
		}
	}
	sort.Strings(targets)
	if targets == nil {
		return []string{}
	}
	return uniqueStrings(targets)
}

func deriveABI(vdbRoot string, packages []vdb.Package) (ABI, error) {
	var perlPackages []vdb.Package
	for _, pkg := range packages {
		if pkg.CP() == "dev-lang/perl" {
			perlPackages = append(perlPackages, pkg)
		}
	}
	if len(perlPackages) != 1 {
		return ABI{}, fmt.Errorf("perl-cleaner: expected exactly one installed dev-lang/perl, found %d", len(perlPackages))
	}
	pkg := perlPackages[0]
	version := pkg.Subslot
	if version == "" {
		return ABI{}, fmt.Errorf("perl-cleaner: installed %s has no Perl ABI subslot", pkg.CPV())
	}
	arch := ""
	prefixes := []string{"/usr/lib/perl5/" + version + "/", "/usr/lib64/perl5/" + version + "/"}
	for _, path := range perlModulePaths(pkg.Contents) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(path, prefix) {
				rest := strings.TrimPrefix(path, prefix)
				candidate, _, found := strings.Cut(rest, "/")
				if found && strings.Contains(candidate, "-") {
					arch = candidate
					break
				}
			}
		}
		if arch != "" {
			break
		}
	}
	sonames, err := providedLibPerl(filepath.Join(vdbRoot, pkg.Category, pkg.Package+"-"+pkg.Version, "NEEDED.ELF.2"))
	if err != nil {
		return ABI{}, fmt.Errorf("perl-cleaner: derive libperl ABI: %w", err)
	}
	if len(sonames) == 0 {
		return ABI{}, fmt.Errorf("perl-cleaner: installed %s provides no libperl SONAME", pkg.CPV())
	}
	return ABI{Version: version, Arch: arch, LibPerlSONames: sonames, SourceCPV: pkg.CPV()}, nil
}

func perlModulePaths(contents string) []string {
	var paths []string
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || (fields[0] != "obj" && fields[0] != "sym") {
			continue
		}
		path := fields[1]
		if isPerlModulePath(path) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func isPerlModulePath(path string) bool {
	for _, prefix := range []string{"/usr/share/perl5/", "/usr/lib/perl5/", "/usr/lib32/perl5/", "/usr/lib64/perl5/", "/usr/libx32/perl5/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func staleModulePath(path string, abi ABI) bool {
	match := perlVersionSegment.FindStringSubmatch(path)
	if len(match) != 2 || match[1] != abi.Version {
		return true
	}
	if abi.Arch == "" {
		return false
	}
	after := path[strings.Index(path, match[0])+len(match[0]):]
	first, _, _ := strings.Cut(after, "/")
	return looksLikeArch(first, abi.Arch) && first != abi.Arch
}

func looksLikeArch(segment, active string) bool {
	if segment == active {
		return true
	}
	_, osName, found := strings.Cut(active, "-")
	if !found || osName == "" {
		return false
	}
	return strings.HasSuffix(segment, "-"+osName) &&
		(strings.Contains(segment, "_") || strings.HasPrefix(segment, "i") ||
			strings.HasPrefix(segment, "arm") || strings.HasPrefix(segment, "aarch"))
}

func providedLibPerl(path string) ([]string, error) {
	records, err := neededRecords(path)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, record := range records {
		if strings.HasPrefix(record.provided, "libperl.") {
			result = append(result, record.provided)
		}
	}
	sort.Strings(result)
	return uniqueStrings(result), nil
}

func linkedLibPerl(path string) ([]string, error) {
	records, err := neededRecords(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []string
	for _, record := range records {
		for _, needed := range strings.Split(record.needed, ",") {
			if strings.HasPrefix(needed, "libperl.") {
				result = append(result, needed)
			}
		}
	}
	sort.Strings(result)
	return uniqueStrings(result), nil
}

type neededRecord struct {
	provided string
	needed   string
}

func neededRecords(path string) ([]neededRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []neededRecord
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ";")
		if len(fields) != 6 {
			return nil, fmt.Errorf("malformed NEEDED.ELF.2 record %q", scanner.Text())
		}
		records = append(records, neededRecord{provided: fields[2], needed: fields[4]})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func excluded(cp string) bool {
	return cp == "dev-lang/perl" || cp == "sys-devel/libperl" || cp == "app-emulation/emul-linux-x86-baselibs"
}

func uniqueReasons(reasons []Reason) []Reason {
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Kind != reasons[j].Kind {
			return reasons[i].Kind < reasons[j].Kind
		}
		return reasons[i].Evidence < reasons[j].Evidence
	})
	result := reasons[:0]
	for _, reason := range reasons {
		if len(result) == 0 || result[len(result)-1] != reason {
			result = append(result, reason)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
