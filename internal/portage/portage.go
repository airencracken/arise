package portage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

type Config struct {
	MakeConf map[string]string

	USE             []string
	CFLAGS          string
	CXXFLAGS        string
	MAKEOPTS        string
	ACCEPT_KEYWORDS []string
	ACCEPT_LICENSE  []string
	FEATURES        []string

	PackageUse            map[string][]string
	PackageAcceptKeywords map[string]string
	PackageLicense        map[string]string
	PackageMask           []string
	PackageUnmask         []string
	PackageEnv            map[string]string
	PackageProvided       []string
}

func LoadConfig(portageConfigRoot string) (*Config, error) {
	cfg := &Config{
		MakeConf:              make(map[string]string),
		PackageUse:            make(map[string][]string),
		PackageAcceptKeywords: make(map[string]string),
		PackageLicense:        make(map[string]string),
		PackageEnv:            make(map[string]string),
	}

	if _, err := os.Stat(portageConfigRoot); os.IsNotExist(err) {
		return cfg, nil
	}

	mc, err := ParseMakeConf(filepath.Join(portageConfigRoot, "make.conf"))
	if err != nil {
		return nil, fmt.Errorf("portage: %w", err)
	}
	if mc != nil {
		cfg.MakeConf = mc
		ResolveMakeConfRefs(cfg.MakeConf)
	}

	cfg.populateAccessors()

	if err := cfg.loadPackageFiles(portageConfigRoot); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) populateAccessors() {
	if v, ok := cfg.MakeConf["USE"]; ok {
		cfg.USE = splitShWords(v)
	}
	if v, ok := cfg.MakeConf["CFLAGS"]; ok {
		cfg.CFLAGS = v
	}
	if v, ok := cfg.MakeConf["CXXFLAGS"]; ok {
		cfg.CXXFLAGS = v
	}
	if v, ok := cfg.MakeConf["MAKEOPTS"]; ok {
		cfg.MAKEOPTS = v
	}
	if v, ok := cfg.MakeConf["ACCEPT_KEYWORDS"]; ok {
		cfg.ACCEPT_KEYWORDS = splitShWords(v)
	}
	if v, ok := cfg.MakeConf["ACCEPT_LICENSE"]; ok {
		cfg.ACCEPT_LICENSE = splitShWords(v)
	}
	if v, ok := cfg.MakeConf["FEATURES"]; ok {
		cfg.FEATURES = splitShWords(v)
	}
}

func (cfg *Config) loadPackageFiles(root string) error {
	var err error

	cfg.PackageUse, err = ParsePackageUse(filepath.Join(root, "package.use"))
	if err != nil {
		return fmt.Errorf("package.use: %w", err)
	}

	cfg.PackageAcceptKeywords, err = ParsePackageAcceptKeywords(filepath.Join(root, "package.accept_keywords"))
	if err != nil {
		return fmt.Errorf("package.accept_keywords: %w", err)
	}

	cfg.PackageLicense, err = ParsePackageLicense(filepath.Join(root, "package.license"))
	if err != nil {
		return fmt.Errorf("package.license: %w", err)
	}

	cfg.PackageMask, err = ParsePackageMask(filepath.Join(root, "package.mask"))
	if err != nil {
		return fmt.Errorf("package.mask: %w", err)
	}

	cfg.PackageUnmask, err = ParsePackageUnmask(filepath.Join(root, "package.unmask"))
	if err != nil {
		return fmt.Errorf("package.unmask: %w", err)
	}

	cfg.PackageEnv, err = ParsePackageEnv(filepath.Join(root, "package.env"))
	if err != nil {
		return fmt.Errorf("package.env: %w", err)
	}

	cfg.PackageProvided, err = ParsePackageProvided(filepath.Join(root, "profile", "package.provided"))
	if err != nil {
		return fmt.Errorf("profile/package.provided: %w", err)
	}

	return nil
}

func ParseMakeConf(path string) (map[string]string, error) {
	lines, err := readMakeConfLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	m := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = unquote(val)
		if key != "" {
			m[key] = val
		}
	}
	return m, nil
}

func readMakeConfLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var buf strings.Builder
	for scanner.Scan() {
		raw := scanner.Text()
		stripped := strings.TrimRight(raw, " \t")

		if len(stripped) > 0 && stripped[len(stripped)-1] == '\\' && !strings.HasSuffix(stripped, "\\\\") {
			buf.WriteString(stripped[:len(stripped)-1])
			continue
		}

		if buf.Len() > 0 {
			buf.WriteString(raw)
			lines = append(lines, buf.String())
			buf.Reset()
		} else {
			lines = append(lines, raw)
		}
	}
	if buf.Len() > 0 {
		lines = append(lines, buf.String())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return lines, nil
}

func ParsePackageUse(path string) (map[string][]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string][]string)
	for _, line := range lines {
		atom, flagsStr := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		flags := splitShWords(flagsStr)
		if len(flags) == 0 {
			continue
		}
		m[atom] = append(m[atom], flags...)
	}
	return m, nil
}

func ParsePackageAcceptKeywords(path string) (map[string]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string]string)
	for _, line := range lines {
		atom, keyword := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		m[atom] = keyword
	}
	return m, nil
}

func ParsePackageLicense(path string) (map[string]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string]string)
	for _, line := range lines {
		atom, license := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		m[atom] = license
	}
	return m, nil
}

func ParsePackageMask(path string) ([]string, error) {
	return parseAtomList(path)
}

func ParsePackageUnmask(path string) ([]string, error) {
	return parseAtomList(path)
}

func parseAtomList(path string) ([]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	var atoms []string
	for _, line := range lines {
		atom, _ := parseAtomConfig(line)
		if atom != "" {
			atoms = append(atoms, atom)
		}
	}
	return atoms, nil
}

// ParsePackageProvided reads a package.provided file (or directory) and
// returns the list of package atoms that are declared as externally provided.
func ParsePackageProvided(path string) ([]string, error) {
	return parseAtomList(path)
}

func ParsePackageEnv(path string) (map[string]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string]string)
	for _, line := range lines {
		atom, envFile := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		m[atom] = envFile
	}
	return m, nil
}

func ResolveMakeConfRefs(m map[string]string) {
	if m == nil {
		return
	}

	const maxIter = 24
	for i := 0; i < maxIter; i++ {
		changed := false
		for key, val := range m {
			matches := refPattern.FindAllStringSubmatchIndex(val, -1)
			if len(matches) == 0 {
				continue
			}

			var result strings.Builder
			lastEnd := 0
			for _, match := range matches {
				fullStart, fullEnd := match[0], match[1]
				refStart, refEnd := match[2], match[3]
				varName := val[refStart:refEnd]

				result.WriteString(val[lastEnd:fullStart])

				if varName == key {
					result.WriteString("")
				} else if refVal, ok := m[varName]; ok {
					result.WriteString(refVal)
				} else {
					result.WriteString(val[fullStart:fullEnd])
				}
				lastEnd = fullEnd
			}
			result.WriteString(val[lastEnd:])

			newVal := result.String()
			if newVal != val {
				m[key] = newVal
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func ReadConfigFile(path string) ([]string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var files []string
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			files = append(files, filepath.Join(path, e.Name()))
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}

	var allLines []string
	for _, f := range files {
		lines, err := readFileLines(f)
		if err != nil {
			return nil, err
		}
		allLines = append(allLines, lines...)
	}
	return allLines, nil
}

func readFileLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return lines, nil
}

func splitShWords(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	var words []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			} else {
				current.WriteByte(ch)
			}
		case inDouble:
			if ch == '"' {
				inDouble = false
			} else {
				current.WriteByte(ch)
			}
		case ch == '\'':
			inSingle = true
		case ch == '"':
			inDouble = true
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

func parseAtomConfig(line string) (atom, value string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}

	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			return line[:i], strings.TrimSpace(line[i:])
		}
	}
	return line, ""
}

func unquote(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
			(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
			return trimmed[1 : len(trimmed)-1]
		}
	}
	return s
}

// ParseBinhostConfig reads binhost URLs from portage config.
func ParseBinhostConfig(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	if v, ok := cfg.MakeConf["PORTAGE_BINHOST"]; ok && v != "" {
		return splitShWords(v)
	}
	return nil
}
