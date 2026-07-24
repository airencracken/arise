// Package repoaudit performs read-only compatibility audits of a Portage tree.
package repoaudit

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/ebuild"
)

type Location struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

type ParseFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type MissingInherit struct {
	Location
	Eclass string `json:"eclass"`
}

type InheritDifference struct {
	Path       string   `json:"path"`
	ParserOnly []string `json:"parser_only,omitempty"`
	StaticOnly []string `json:"static_only,omitempty"`
}

type QueryUse struct {
	Location
	Function       string `json:"function"`
	Argument       string `json:"argument"`
	Classification string `json:"classification"`
}

type HelperCoverage struct {
	Helper            string `json:"helper"`
	Uses              int    `json:"uses"`
	Implemented       bool   `json:"implemented"`
	IntentionallyBans bool   `json:"intentionally_banned,omitempty"`
}

type Report struct {
	Repository         string              `json:"repository"`
	Ebuilds            int                 `json:"ebuilds"`
	Eclasses           int                 `json:"eclasses"`
	EAPIs              map[string]int      `json:"eapis"`
	ParseFailures      []ParseFailure      `json:"parse_failures,omitempty"`
	MissingInherits    []MissingInherit    `json:"missing_inherits,omitempty"`
	InheritDifferences []InheritDifference `json:"inherit_differences,omitempty"`
	InheritCycles      [][]string          `json:"inherit_cycles,omitempty"`
	Queries            []QueryUse          `json:"queries,omitempty"`
	QueryClasses       map[string]int      `json:"query_classes"`
	HelperCoverage     []HelperCoverage    `json:"helper_coverage"`
}

var (
	staticInheritRE = regexp.MustCompile(`^[[:space:]]*inherit[[:space:]]+([^#\r\n]+)`)
	safeEclassRE    = regexp.MustCompile(`^[A-Za-z0-9+_.-]+$`)
	queryRE         = regexp.MustCompile(`\b(has_version|best_version|python_has_version)[[:space:]]+(?:-[bdr][[:space:]]+)?([^[:space:];|&)]+)`)
	functionRE      = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_.+-]*)\(\)[[:space:]]*(?:\{|\()`)
	bannedHelperRE  = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_.+-]*)\(\).*[[:space:]]is banned(?:[[:space:]]|$)`)
)

// The list is deliberately limited to the package-manager supplied interface.
// Eclass-defined functions are not expected to exist in the phase worker.
var portageHelpers = []string{
	"EXPORT_FUNCTIONS", "assert", "best_version", "contains_word", "debug-print",
	"debug-print-function", "default", "die", "dobin", "docompress", "doconfd",
	"dodir", "dodoc", "doenvd", "doexe", "dohard", "doheader", "dohtml",
	"doinfo", "doinitd", "doins", "dolib", "dolib.a", "dolib.so", "doman",
	"domo", "dosbin", "dosed", "dosym", "dostrip", "eapply", "eapply_user",
	"ebegin", "econf", "eend", "eerror", "einfo", "einstalldocs", "elog",
	"emake", "eqatag", "eqawarn", "ewarn", "exeinto", "exeopts", "find0",
	"fowners", "fperms", "get_libdir", "has", "has_version", "inherit",
	"in_iuse", "insinto", "insopts", "into", "keepdir", "newbin", "newconfd",
	"newdoc", "newenvd", "newexe", "newheader", "newinitd", "newins",
	"newlib.a", "newlib.so", "newman", "newsbin", "nonfatal", "pipestatus",
	"unpack", "use", "use_enable", "use_with", "usev", "usex", "ver_cut",
	"ver_replacing", "ver_rs", "ver_test",
}

type sourceFile struct {
	path     string
	relative string
	kind     string
	data     string
	static   []string
}

func Run(repository, workerPath string) (*Report, error) {
	repository, err := filepath.Abs(repository)
	if err != nil {
		return nil, err
	}
	eclassDir := filepath.Join(repository, "eclass")
	eclassEntries, err := os.ReadDir(eclassDir)
	if err != nil {
		return nil, fmt.Errorf("repo audit: read eclass directory: %w", err)
	}
	available := make(map[string]bool)
	for _, entry := range eclassEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".eclass") {
			available[strings.TrimSuffix(entry.Name(), ".eclass")] = true
		}
	}

	var files []sourceFile
	err = filepath.WalkDir(repository, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "metadata" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".ebuild" && ext != ".eclass" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, _ := filepath.Rel(repository, path)
		files = append(files, sourceFile{
			path: path, relative: filepath.ToSlash(relative),
			kind: strings.TrimPrefix(ext, "."), data: string(data),
			static: staticInherits(string(data)),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("repo audit: walk repository: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })

	report := &Report{
		Repository:   repository,
		EAPIs:        make(map[string]int),
		QueryClasses: make(map[string]int),
	}
	graph := make(map[string][]string)
	helperUses := make(map[string]int)
	helperSet := make(map[string]bool, len(portageHelpers))
	for _, helper := range portageHelpers {
		helperSet[helper] = true
	}

	for _, file := range files {
		if file.kind == "ebuild" {
			report.Ebuilds++
		} else {
			report.Eclasses++
			graph[strings.TrimSuffix(filepath.Base(file.path), ".eclass")] = file.static
		}
		parsed, parseErr := ebuild.ParseEbuild(file.path)
		if parseErr != nil {
			report.ParseFailures = append(report.ParseFailures, ParseFailure{Path: file.relative, Error: parseErr.Error()})
		} else {
			if file.kind == "ebuild" {
				eapi := parsed.EAPI
				if eapi == "" {
					eapi = "<unset>"
				}
				report.EAPIs[eapi]++
			}
			parserOnly, staticOnly := difference(parsed.Inherit, file.static)
			if len(parserOnly) != 0 || len(staticOnly) != 0 {
				report.InheritDifferences = append(report.InheritDifferences, InheritDifference{
					Path: file.relative, ParserOnly: parserOnly, StaticOnly: staticOnly,
				})
			}
		}
		for lineNumber, line := range strings.Split(file.data, "\n") {
			for _, inherited := range staticInherits(line) {
				if !available[inherited] {
					report.MissingInherits = append(report.MissingInherits, MissingInherit{
						Location: Location{Path: file.relative, Line: lineNumber + 1, Text: strings.TrimSpace(line)},
						Eclass:   inherited,
					})
				}
			}
			for _, match := range queryRE.FindAllStringSubmatch(line, -1) {
				argument := strings.Trim(match[2], `"'`)
				classification := classifyQuery(argument)
				report.QueryClasses[classification]++
				report.Queries = append(report.Queries, QueryUse{
					Location: Location{Path: file.relative, Line: lineNumber + 1, Text: strings.TrimSpace(line)},
					Function: match[1], Argument: argument, Classification: classification,
				})
			}
			for _, helper := range portageHelpers {
				if containsShellWord(line, helper) {
					helperUses[helper]++
				}
			}
		}
	}
	report.InheritCycles = findCycles(graph)

	implemented, banned, err := workerFunctions(workerPath)
	if err != nil {
		return nil, err
	}
	for _, helper := range portageHelpers {
		report.HelperCoverage = append(report.HelperCoverage, HelperCoverage{
			Helper: helper, Uses: helperUses[helper],
			Implemented: implemented[helper], IntentionallyBans: banned[helper],
		})
	}
	return report, nil
}

func staticInherits(source string) []string {
	seen := make(map[string]bool)
	var result []string
	scanner := bufio.NewScanner(strings.NewReader(source))
	for scanner.Scan() {
		match := staticInheritRE.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		for _, name := range strings.Fields(match[1]) {
			if safeEclassRE.MatchString(name) && !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
	}
	sort.Strings(result)
	return result
}

func difference(left, right []string) ([]string, []string) {
	lset, rset := make(map[string]bool), make(map[string]bool)
	for _, value := range left {
		lset[value] = true
	}
	for _, value := range right {
		rset[value] = true
	}
	var leftOnly, rightOnly []string
	for value := range lset {
		if !rset[value] {
			leftOnly = append(leftOnly, value)
		}
	}
	for value := range rset {
		if !lset[value] {
			rightOnly = append(rightOnly, value)
		}
	}
	sort.Strings(leftOnly)
	sort.Strings(rightOnly)
	return leftOnly, rightOnly
}

func classifyQuery(argument string) string {
	if strings.Contains(argument, "$(") || strings.Contains(argument, "`") {
		return "computed"
	}
	if strings.Contains(argument, "$") {
		return "variable"
	}
	if argument == "" {
		return "missing"
	}
	return "literal"
}

func containsShellWord(line, word string) bool {
	for index := strings.Index(line, word); index >= 0; {
		beforeOK := index == 0 || !isShellNameByte(line[index-1])
		end := index + len(word)
		afterOK := end == len(line) || !isShellNameByte(line[end])
		if beforeOK && afterOK {
			return true
		}
		next := strings.Index(line[index+1:], word)
		if next < 0 {
			return false
		}
		index += next + 1
	}
	return false
}

func isShellNameByte(value byte) bool {
	return value == '_' || value == '.' || value == '+' || value == '-' ||
		value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func workerFunctions(path string) (map[string]bool, map[string]bool, error) {
	implemented, banned := make(map[string]bool), make(map[string]bool)
	if path == "" {
		return implemented, banned, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("repo audit: read phase worker: %w", err)
	}
	for _, match := range functionRE.FindAllStringSubmatch(string(data), -1) {
		implemented[match[1]] = true
	}
	for _, match := range bannedHelperRE.FindAllStringSubmatch(string(data), -1) {
		banned[match[1]] = true
	}
	return implemented, banned, nil
}

func findCycles(graph map[string][]string) [][]string {
	state := make(map[string]uint8)
	var stack []string
	var cycles [][]string
	var visit func(string)
	visit = func(name string) {
		switch state[name] {
		case 1:
			for index, item := range stack {
				if item == name {
					cycle := append([]string(nil), stack[index:]...)
					cycle = append(cycle, name)
					cycles = append(cycles, cycle)
					break
				}
			}
			return
		case 2:
			return
		}
		state[name] = 1
		stack = append(stack, name)
		for _, dependency := range graph[name] {
			if _, exists := graph[dependency]; exists {
				visit(dependency)
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
	}
	var names []string
	for name := range graph {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		visit(name)
	}
	return cycles
}
