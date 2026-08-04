package news

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/gentooling"
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

func ReadNews(newsDir string) ([]NewsItem, error) {
	state, err := gentooling.ReadNewsState(context.Background(), gentooling.NewsPaths{
		RepositoryName: "gentoo", NewsDirectory: newsDir,
		StateDirectory: filepath.Join(newsDir, ".arise-no-news-state"),
	}, gentooling.NewsContext{})
	if err != nil {
		return nil, err
	}
	items := make([]NewsItem, 0, len(state.Items))
	for _, item := range state.Items {
		installed := ""
		if len(item.DisplayIfInstalled) != 0 {
			installed = item.DisplayIfInstalled[0]
		}
		items = append(items, NewsItem{
			Path: filepath.Dir(item.Path), Title: item.Title, Author: item.Author,
			Date: item.Date, Revision: item.Revision, NewsItemFormat: item.Format,
			DisplayIfInstall: installed, Body: item.Body,
		})
	}
	return items, nil
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
	portageUnread := filepath.Join(readMarkerDir, "news-gentoo.unread")
	if _, err := os.Stat(portageUnread); err == nil {
		state, err := gentooling.ReadNewsState(context.Background(), gentooling.NewsPaths{
			RepositoryName: "gentoo", NewsDirectory: newsDir, StateDirectory: readMarkerDir,
		}, gentooling.NewsContext{})
		if err != nil {
			return nil, err
		}
		unread := make([]NewsItem, 0, len(state.Unread))
		for _, item := range state.Unread {
			unread = append(unread, NewsItem{Path: filepath.Dir(item.Path), Title: item.Title, Author: item.Author, Date: item.Date, Revision: item.Revision, NewsItemFormat: item.Format, Body: item.Body})
		}
		return unread, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("news: could not inspect Portage unread state: %w", err)
	}
	all, err := ReadNews(newsDir)
	if err != nil {
		return nil, err
	}

	// Portage records the relevant unread item IDs in a repository-specific
	// file. Prefer that authoritative set when present so irrelevant GLEP 42
	// items are not reported as unread.
	readNames, err := readMarkerNames(readMarkerDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("news: could not read news markers: %w", err)
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
		return fmt.Errorf("news: could not create marker directory %s: %w", readMarkerDir, err)
	}

	portageUnread := filepath.Join(readMarkerDir, "news-gentoo.unread")
	if data, err := os.ReadFile(portageUnread); err == nil {
		markerName := filepath.Base(item.Path)
		var retained []string
		for _, line := range strings.Split(string(data), "\n") {
			name := strings.TrimSpace(line)
			if name != "" && name != markerName {
				retained = append(retained, name)
			}
		}
		contents := ""
		if len(retained) != 0 {
			contents = strings.Join(retained, "\n") + "\n"
		}
		return atomicWrite(portageUnread, []byte(contents), 0644)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("news: could not update Portage unread state: %w", err)
	}

	markerName := filepath.Base(item.Path)
	markerPath := filepath.Join(readMarkerDir, markerName)

	f, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("news: could not mark news item %s as read: %w", markerName, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()

	_, err = f.WriteString(fmt.Sprintf("%s\n", item.Title))
	return err
}

func atomicWrite(path string, data []byte, mode os.FileMode) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".arise-news-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(data)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (n NewsItem) String() string {
	return fmt.Sprintf("%s: %s (rev %d)", n.Date, n.Title, n.Revision)
}
