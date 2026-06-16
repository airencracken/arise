package eclass

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type EclassInfo struct {
	Name      string            // eclass name (e.g., "flag-o-matic")
	Path      string            // path to the eclass file
	EAPI      string            // minimum EAPI if declared
	Variables map[string]string // global variables set by the eclass
	Functions map[string]string // function name -> function body
	Inherits  []string          // what OTHER eclasses this one inherits
}

func LoadEclass(path string) (*EclassInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eclass: could not read eclass file %s: %w", path, err)
	}

	name := strings.TrimSuffix(filepath.Base(path), ".eclass")

	info := &EclassInfo{
		Name:      name,
		Path:      path,
		Variables: make(map[string]string),
		Functions: make(map[string]string),
	}

	lines := mergeContinuations(string(data))
	if err := parseEclassContent(lines, info); err != nil {
		return nil, fmt.Errorf("eclass: could not parse eclass file %s: %w", path, err)
	}

	return info, nil
}

func LoadEclassByName(name string, repoDir string) (*EclassInfo, error) {
	eclassDir := filepath.Join(repoDir, "eclass")
	path := filepath.Join(eclassDir, name+".eclass")
	return LoadEclass(path)
}

func ExpandInherit(inheritList []string, repoDir string) (map[string]string, map[string]string, error) {
	variables := make(map[string]string)
	functions := make(map[string]string)

	eclassDir := filepath.Join(repoDir, "eclass")

	// Build ordered list: resolve dependencies first (topological order).
	// Base eclasses come first, derived eclasses later.
	// Later entries override earlier ones in variables/functions.
	var order []string
	loaded := make(map[string]bool)
	if err := resolveEclassOrder(inheritList, eclassDir, loaded, &order); err != nil {
		return nil, nil, err
	}

	for _, name := range order {
		path := filepath.Join(eclassDir, name+".eclass")
		info, err := LoadEclass(path)
		if err != nil {
			return nil, nil, fmt.Errorf("eclass: could not load inherited eclass %s: %w", name, err)
		}

		// variables from later eclasses (in inherit order) override earlier ones
		for k, v := range info.Variables {
			variables[k] = v
		}

		// collect exported functions (from EXPORT_FUNCTIONS)
		exportNames := parseExportFunctions(info.Variables["EXPORT_FUNCTIONS"])
		for _, fnName := range exportNames {
			if body, ok := info.Functions[fnName]; ok {
				functions[fnName] = body
			}
		}
	}

	return variables, functions, nil
}

func resolveEclassOrder(names []string, eclassDir string, loaded map[string]bool, order *[]string) error {
	for _, name := range names {
		if loaded[name] {
			continue
		}

		path := filepath.Join(eclassDir, name+".eclass")
		info, err := LoadEclass(path)
		if err != nil {
			return fmt.Errorf("eclass: could not resolve eclass dependency %s: %w", name, err)
		}

		loaded[name] = true

		// check for circular deps
		for _, dep := range info.Inherits {
			if loaded[dep] {
				return fmt.Errorf("eclass: circular inherit detected: %s already loaded via %s", dep, name)
			}
		}

		// resolve dependencies first (base classes before derived)
		if err := resolveEclassOrder(info.Inherits, eclassDir, loaded, order); err != nil {
			return err
		}

		*order = append(*order, name)
	}
	return nil
}

func mergeContinuations(raw string) []string {
	rawLines := strings.Split(raw, "\n")
	var out []string
	var buf strings.Builder

	for i := 0; i < len(rawLines); i++ {
		line := rawLines[i]
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, "\\") {
			buf.WriteString(strings.TrimSuffix(trimmed, "\\"))
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString(line)
			out = append(out, buf.String())
			buf.Reset()
		} else {
			out = append(out, line)
		}
	}

	if buf.Len() > 0 {
		out = append(out, buf.String())
	}

	return out
}

func parseEclassContent(lines []string, info *EclassInfo) error {
	condDepth := 0
	inFunc := false
	funcName := ""
	funcLines := make([]string, 0, 32)
	braceDepth := 0
	var rest string

	for _, rawLine := range lines {
		line := rawLine
		trimmed := strings.TrimSpace(line)

		if inFunc {
			funcLines = append(funcLines, line)
			braceDepth += countBracesNoComment(line)
			if braceDepth <= 0 {
				info.Functions[funcName] = strings.Join(funcLines, "\n")
				inFunc = false
				funcName = ""
				funcLines = funcLines[:0]
				braceDepth = 0
			}
			continue
		}

		if condDepth > 0 {
			first := firstWord(trimmed)
			switch first {
			case "if", "for", "while", "until", "case":
				condDepth++
			case "fi", "done", "esac":
				condDepth--
			}
			if condDepth < 0 {
				condDepth = 0
			}
			continue
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		first := firstWord(trimmed)
		switch first {
		case "if", "for", "while", "until", "case":
			condDepth = 1
			closes := countCondCloses(trimmed)
			condDepth -= closes
			if condDepth < 0 {
				condDepth = 0
			}
			continue
		}

		if strings.HasPrefix(trimmed, "inherit ") {
			parts := strings.Fields(trimmed[len("inherit "):])
			info.Inherits = append(info.Inherits, parts...)
			continue
		}

		if isFuncDef(trimmed) {
			funcName, rest = parseFuncDef(trimmed)
			if strings.Contains(rest, "{") {
				funcLines = append(funcLines[:0], line)
				inFunc = true
				braceDepth = countBracesNoComment(line)
				if braceDepth <= 0 {
					info.Functions[funcName] = strings.Join(funcLines, "\n")
					inFunc = false
					funcName = ""
					funcLines = funcLines[:0]
					braceDepth = 0
				}
			}
			continue
		}

		if name, value, ok := parseVarAssign(trimmed); ok {
			info.Variables[name] = value
		}
	}

	if inFunc {
		return fmt.Errorf("eclass: unclosed function definition %q", funcName)
	}
	if braceDepth > 0 {
		return fmt.Errorf("eclass: unclosed braces at depth %d", braceDepth)
	}

	if v, ok := info.Variables["EAPI"]; ok {
		info.EAPI = v
	}

	return nil
}

func isFuncDef(trimmed string) bool {
	parenIdx := strings.Index(trimmed, "()")
	if parenIdx < 0 {
		return false
	}
	name := strings.TrimSpace(trimmed[:parenIdx])
	return isFuncName(name)
}

func parseFuncDef(trimmed string) (name, rest string) {
	parenIdx := strings.Index(trimmed, "()")
	if parenIdx < 0 {
		return "", trimmed
	}
	name = strings.TrimSpace(trimmed[:parenIdx])
	rest = strings.TrimSpace(trimmed[parenIdx+2:])
	return
}

func parseVarAssign(trimmed string) (name, value string, ok bool) {
	hasExport := false
	if after, found := strings.CutPrefix(trimmed, "export "); found {
		trimmed = strings.TrimSpace(after)
		hasExport = true
	}

	if idx := strings.IndexByte(trimmed, '='); idx > 0 {
		name = strings.TrimSpace(trimmed[:idx])
		value = strings.TrimSpace(trimmed[idx+1:])
		if name == "" || !isBashName(name) {
			return "", "", false
		}
		return name, value, true
	}

	if hasExport && trimmed != "" && isBashName(trimmed) {
		return trimmed, "", true
	}

	return "", "", false
}

func parseExportFunctions(rawValue string) []string {
	value := strings.TrimSpace(rawValue)
	value = stripQuotes(value)
	if value == "" {
		return nil
	}
	return strings.Fields(value)
}

// isBashName checks for valid bash variable names (no hyphens).
func isBashName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 && !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
			return false
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

// isFuncName checks for valid bash function names (hyphens allowed for eclass conventions).
func isFuncName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 && !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
			return false
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func stripQuotes(s string) string {
	for len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
		} else {
			break
		}
	}
	return s
}

func countBracesNoComment(line string) int {
	commentIdx := strings.IndexByte(line, '#')
	effective := line
	if commentIdx >= 0 {
		effective = line[:commentIdx]
	}
	return strings.Count(effective, "{") - strings.Count(effective, "}")
}

func firstWord(s string) string {
	if idx := strings.IndexAny(s, " \t;{("); idx >= 0 {
		return s[:idx]
	}
	return s
}

func countCondCloses(trimmed string) int {
	first := firstWord(trimmed)
	switch first {
	case "fi", "done", "esac":
		return 1
	}
	return 0
}
