package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/news"
)

func runNews(args []string) {
	newsDir := filepath.Join(*repoPath, "metadata", "news")
	markerDir := "/var/lib/gentoo/news"
	subCmd := "list"
	if len(args) > 0 {
		subCmd = args[0]
		args = args[1:]
	}

	switch subCmd {
	case "list":
		items, err := news.ReadNews(newsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: %v\n", err)
			os.Exit(1)
		}

		if len(items) == 0 {
			fmt.Println("No news items found.")
			return
		}

		unread, err := news.ReadUnreadNews(newsDir, markerDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: unread state: %v\n", err)
			os.Exit(1)
		}
		writeNewsList(os.Stdout, items, unread)
	case "read":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "news read: missing news item specifier\n")
			os.Exit(1)
		}

		items, err := news.ReadNews(newsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: list: %v\n", err)
			os.Exit(1)
		}

		// "all" marks everything as read
		if args[0] == "all" {
			for _, item := range items {
				if err := news.MarkRead(markerDir, item); err != nil {
					fmt.Fprintf(os.Stderr, "news: mark read: %v\n", err)
					os.Exit(1)
				}
			}
			fmt.Printf("Marked %d news items as read.\n", len(items))
			return
		}

		item, err := selectNewsItem(items, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "news read: %v\n", err)
			os.Exit(1)
		}
		if err := news.MarkRead(markerDir, item); err != nil {
			fmt.Fprintf(os.Stderr, "news: mark read: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Marked news item %s as read.\n", color.Bold(args[0]))
	case "display":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "news display: missing news item number or specifier\n")
			os.Exit(1)
		}

		allItems, err := news.ReadNews(newsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: %v\n", err)
			os.Exit(1)
		}
		item, err := selectNewsItem(allItems, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "news display: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n  %s %s\n", color.BoldCyan("Title:"), color.Bold(item.Title))
		fmt.Printf("  %s %s\n", color.Bold("Author:"), item.Author)
		fmt.Printf("  %s %s\n", color.Bold("Date:"), item.Date)
		fmt.Printf("  %s %s\n", color.Bold("Format:"), item.NewsItemFormat)
		fmt.Printf("\n%s\n", item.Body)
		if err := news.MarkRead(markerDir, item); err != nil {
			fmt.Fprintf(os.Stderr, "news: mark read: %v\n", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "news: unknown subcommand %q\n", subCmd)
		fmt.Fprintf(os.Stderr, "Usage: arise news [list|read|display]\n")
		os.Exit(1)
	}
}

func writeNewsList(writer io.Writer, items, unread []news.NewsItem) {
	unreadPaths := make(map[string]bool, len(unread))
	for _, item := range unread {
		unreadPaths[item.Path] = true
	}
	for index, item := range items {
		number := color.Cyan(fmt.Sprintf("%3d", index+1))
		status := "[ ]"
		if unreadPaths[item.Path] {
			status = color.BoldYellow("[N]")
		}
		fmt.Fprintf(writer, "%s %s %s: %s (rev %d)\n",
			number, status, color.Cyan(item.Date), color.Bold(item.Title), item.Revision)
	}
}

func selectNewsItem(items []news.NewsItem, specifier string) (news.NewsItem, error) {
	if number, err := strconv.Atoi(specifier); err == nil {
		if number < 1 || number > len(items) {
			return news.NewsItem{}, fmt.Errorf("item number %d is outside 1..%d", number, len(items))
		}
		return items[number-1], nil
	}
	for _, item := range items {
		if item.Path == specifier || filepath.Base(item.Path) == specifier {
			return item, nil
		}
	}
	return news.NewsItem{}, fmt.Errorf("unknown news item %q", specifier)
}
