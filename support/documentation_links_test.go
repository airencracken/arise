package support

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Check local Markdown paths throughout the repository, including dated
// records. This is offline: remote availability is not inferred from a URL.
func TestDocumentationLinks(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	linkRE := regexp.MustCompile(`\[[^\]\n]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)`)
	files, links := 0, 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range linkRE.FindAllStringSubmatch(string(data), -1) {
			target := match[1]
			if strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			if strings.ContainsAny(target, "<>*{}") {
				continue
			}
			target = strings.ReplaceAll(target, "%20", " ")
			links++
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), target)); err != nil {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s: broken local link %q: %v", relative, match[1], err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checked %d Markdown files and %d local link targets", files, links)
}
