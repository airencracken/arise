package support

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPunchlistContainsOnlyActionableOpenWork(t *testing.T) {
	data, err := os.ReadFile("../PUNCHLIST.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Contains(body, "- [x]") || strings.Contains(body, "- [~]") {
		t.Fatal("completed or ambiguous status items belong in history, not the active punchlist")
	}
	starts := regexp.MustCompile(`(?m)^- \[ \] \*\*([SNL][0-9]{2}) — `).FindAllStringSubmatchIndex(body, -1)
	if len(starts) == 0 {
		t.Fatal("no actionable backlog entries")
	}
	seen := map[string]bool{}
	for i, match := range starts {
		id := body[match[2]:match[3]]
		if seen[id] {
			t.Fatalf("duplicate backlog ID %s", id)
		}
		seen[id] = true
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		item := body[match[0]:end]
		if !strings.Contains(item, "**Done when:**") {
			t.Errorf("%s lacks acceptance criteria", id)
		}
		if !strings.Contains(item, "](") {
			t.Errorf("%s lacks implementation/evidence entry point", id)
		}
	}
	links := regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(body, -1)
	for _, link := range links {
		path := strings.SplitN(link[1], "#", 2)[0]
		if path == "" || strings.Contains(path, "://") {
			continue
		}
		if _, err := os.Stat(filepath.Join("..", path)); err != nil {
			t.Errorf("broken punchlist link %s: %v", path, err)
		}
	}
}
