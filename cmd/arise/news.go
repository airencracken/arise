package main

import (
	"fmt"
	"os"
	"path/filepath"

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

		for _, item := range items {
			readStatus := " "
			unread, err := news.ReadUnreadNews(newsDir, markerDir)
			if err == nil {
				isRead := true
				for _, u := range unread {
					if u.Path == item.Path {
						isRead = false
						break
					}
				}
				if !isRead {
					readStatus = "N"
				}
			}
			fmt.Printf("[%s] %s\n", readStatus, item)
		}
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

		fmt.Fprintf(os.Stderr, "news read: specify 'all' to mark all as read, or use item path\n")
		os.Exit(1)
	case "display":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "news display: missing news item count or specifier\n")
			os.Exit(1)
		}

		items, err := news.ReadUnreadNews(newsDir, markerDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: %v\n", err)
			os.Exit(1)
		}

		count := 1
		fmt.Sscanf(args[0], "%d", &count)
		if count > len(items) {
			count = len(items)
		}

		for i := 0; i < count; i++ {
			item := items[i]
			fmt.Printf("\n  Title: %s\n", item.Title)
			fmt.Printf("  Author: %s\n", item.Author)
			fmt.Printf("  Date: %s\n", item.Date)
			fmt.Printf("  Format: %s\n", item.NewsItemFormat)
			fmt.Printf("\n%s\n", item.Body)
		}

		if len(items) > 0 {
			for _, item := range items[:count] {
				if err := news.MarkRead(markerDir, item); err != nil {
					fmt.Fprintf(os.Stderr, "news: mark read: %v\n", err)
				}
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "news: unknown subcommand %q\n", subCmd)
		fmt.Fprintf(os.Stderr, "Usage: arise news [list|read|display]\n")
		os.Exit(1)
	}
}
