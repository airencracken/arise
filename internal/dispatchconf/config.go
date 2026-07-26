package dispatchconf

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadConfig overlays dispatch-conf.conf values onto base.
func LoadConfig(path, eprefix string, base Options) (Options, error) {
	file, err := os.Open(path)
	if err != nil {
		return base, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return base, fmt.Errorf("read %s: %w", path, err)
	}
	required := []string{"archive-dir", "diff", "replace-cvs", "replace-wscomments"}
	for _, key := range required {
		if _, ok := values[key]; !ok {
			return base, fmt.Errorf("%s: missing option %q", path, key)
		}
	}
	expand := func(value string) string {
		return strings.ReplaceAll(value, "${EPREFIX}", eprefix)
	}
	base.ArchiveDir = expand(values["archive-dir"])
	base.DiffCommand = values["diff"]
	if merge := values["merge"]; merge != "" {
		base.MergeCommand = merge
	}
	base.ReplaceCVS = yes(values["replace-cvs"])
	base.ReplaceWSComments = yes(values["replace-wscomments"])
	base.ReplaceUnmodified = yes(values["replace-unmodified"])
	base.IgnorePreviouslyMerged = yes(values["ignore-previously-merged"])
	base.FrozenFiles = strings.Fields(values["frozen-files"])
	if editor := os.Getenv("EDITOR"); editor != "" {
		base.Editor = editor
	}
	if !filepath.IsAbs(base.ArchiveDir) {
		return base, fmt.Errorf("%s: archive-dir must be absolute", path)
	}
	return base, nil
}

func yes(value string) bool { return strings.EqualFold(strings.TrimSpace(value), "yes") }
