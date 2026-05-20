package news

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type NewsItem struct {
	Path             string
	Title            string
	Author           string
	Date             string
	Revision         int
	NewsItemFormat   string
	DisplayIfInstall string
	Body             string
}

var (
	headerPattern = regexp.MustCompile(`^([A-Za-z-]+)\s*:\s*(.*)`)
	datePattern   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)
)

func ReadNews(newsDir string) ([]NewsItem, error) {
	entries, err := os.ReadDir(newsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("news: read dir %s: %w", newsDir, err)
	}

	var items []NewsItem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !datePattern.MatchString(name) {
			continue
		}

		dirPath := filepath.Join(newsDir, name)
		newsFile, err := findNewsTextFile(dirPath)
		if err != nil {
			continue
		}
		if newsFile == "" {
			continue
		}

		item, err := parseNewsFile(filepath.Join(dirPath, newsFile))
		if err != nil {
			continue
		}
		if item == nil {
			continue
		}
		item.Path = dirPath

		items = append(items, *item)
	}

	return items, nil
}

func findNewsTextFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".txt") {
			return name, nil
		}
	}

	return "", nil
}

func parseNewsFile(path string) (*NewsItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	item := &NewsItem{Path: path}

	foundBlank := false
	for i, line := range lines {
		if !foundBlank {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				foundBlank = true
				continue
			}
			matches := headerPattern.FindStringSubmatch(line)
			if len(matches) == 3 {
				key := matches[1]
				val := strings.TrimSpace(matches[2])
				switch key {
				case "Title":
					item.Title = val
				case "Author":
					item.Author = val
				case "Date":
					item.Date = val
				case "Revision":
					if r, err := strconv.Atoi(val); err == nil {
						item.Revision = r
					}
				case "News-Item-Format":
					item.NewsItemFormat = val
				case "Display-If-Installed":
					item.DisplayIfInstall = val
				}
			}
		} else {
			if item.Body != "" {
				item.Body += "\n"
			}
			item.Body += line
			if i == len(lines)-1 && len(remainingLines(lines, i+1)) == 0 {
				break
			}
		}
	}

	item.Body = strings.TrimSpace(item.Body)

	return item, nil
}

func remainingLines(lines []string, from int) []string {
	if from >= len(lines) {
		return nil
	}
	return lines[from:]
}

func ReadNewsForPackage(newsDir string, atom string) ([]NewsItem, error) {
	all, err := ReadNews(newsDir)
	if err != nil {
		return nil, err
	}

	var filtered []NewsItem
	for _, item := range all {
		if item.DisplayIfInstall == "" {
			filtered = append(filtered, item)
			continue
		}
		if matchesConstraint(atom, item.DisplayIfInstall) {
			filtered = append(filtered, item)
		}
	}

	return filtered, nil
}

func matchesConstraint(installed, constraint string) bool {
	ic := strings.SplitN(installed, "/", 2)
	cc := strings.SplitN(constraint, "/", 2)

	if len(ic) < 2 || len(cc) < 2 {
		return false
	}

	if ic[0] != cc[0] {
		return false
	}

	return strings.HasPrefix(ic[1], cc[1])
}

func ReadUnreadNews(newsDir string, readMarkerDir string) ([]NewsItem, error) {
	all, err := ReadNews(newsDir)
	if err != nil {
		return nil, err
	}

	readNames, err := readMarkerNames(readMarkerDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("news: read markers: %w", err)
	}

	readSet := make(map[string]bool, len(readNames))
	for _, name := range readNames {
		readSet[name] = true
	}

	var unread []NewsItem
	for _, item := range all {
		markerName := filepath.Base(item.Path)
		if !readSet[markerName] {
			unread = append(unread, item)
		}
	}

	return unread, nil
}

func readMarkerNames(markerDir string) ([]string, error) {
	entries, err := os.ReadDir(markerDir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}

func MarkRead(readMarkerDir string, item NewsItem) error {
	if err := os.MkdirAll(readMarkerDir, 0755); err != nil {
		return fmt.Errorf("news: create marker dir %s: %w", readMarkerDir, err)
	}

	markerName := filepath.Base(item.Path)
	markerPath := filepath.Join(readMarkerDir, markerName)

	f, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("news: mark read %s: %w", markerName, err)
	}
	defer f.Close()

	_, err = f.WriteString(fmt.Sprintf("%s\n", item.Title))
	return err
}

func (n NewsItem) String() string {
	return fmt.Sprintf("%s: %s (rev %d)", n.Date, n.Title, n.Revision)
}
