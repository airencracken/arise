package distfiles

import (
	"bufio"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// ManifestDownloadSize returns the byte total for enabled SRC_URI entries
// present as DIST records in the selected package's Manifest.
func ManifestDownloadSize(repo, category, pkg, srcURI, distdir string, use map[string]bool) (int64, error) {
	wanted := selectedFiles(srcURI, use)
	if len(wanted) == 0 || repo == "" {
		return 0, nil
	}
	file, err := os.Open(filepath.Join(repo, category, pkg, "Manifest"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var total int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] != "DIST" || !wanted[fields[1]] {
			continue
		}
		if distdir != "" {
			if existing, statErr := os.Stat(filepath.Join(distdir, fields[1])); statErr == nil && !existing.IsDir() {
				continue
			}
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err == nil {
			total += size
		}
	}
	return total, scanner.Err()
}

func selectedFiles(srcURI string, use map[string]bool) map[string]bool {
	tokens := strings.Fields(srcURI)
	selected, _ := parseTokens(tokens, 0, true, use)
	return selected
}

func parseTokens(tokens []string, index int, enabled bool, use map[string]bool) (map[string]bool, int) {
	result := make(map[string]bool)
	for index < len(tokens) {
		token := tokens[index]
		if token == ")" {
			return result, index + 1
		}
		if strings.HasSuffix(token, "?") && index+1 < len(tokens) && tokens[index+1] == "(" {
			flag := strings.TrimSuffix(token, "?")
			condition := use[strings.TrimPrefix(flag, "!")]
			if strings.HasPrefix(flag, "!") {
				condition = !condition
			}
			child, next := parseTokens(tokens, index+2, enabled && condition, use)
			for name := range child {
				result[name] = true
			}
			index = next
			continue
		}
		if enabled && token != "(" && token != "||" {
			name := sourceName(token)
			if index+2 < len(tokens) && tokens[index+1] == "->" {
				name = tokens[index+2]
				index += 2
			}
			if name != "" {
				result[name] = true
			}
		}
		index++
	}
	return result, index
}

func sourceName(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return path.Base(parsed.Path)
}
