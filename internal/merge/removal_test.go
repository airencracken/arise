package merge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemovalPreservesUserChanges(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		for _, change := range []string{"unchanged", "content", "mtime", "type", "symlink", "rollback"} {
			t.Run(fmt.Sprintf("replacement=%t/%s", replacement, change), func(t *testing.T) {
				tmp := t.TempDir()
				root := filepath.Join(tmp, "root")
				image := filepath.Join(tmp, "image")
				vdb := filepath.Join(root, "var/db/pkg")
				journals := filepath.Join(tmp, "journals")
				if err := makeDestDir(image, map[string]string{"etc/example.conf": "original"}); err != nil {
					t.Fatal(err)
				}
				cfg := MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", JournalDir: journals, ConfigProtect: []string{"/etc"}}
				if err := Merge(context.Background(), image, cfg); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "etc/example.conf")
				original, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "content", "rollback":
					if err := os.WriteFile(path, []byte("user changes"), 0644); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "mtime":
					changed := original.ModTime().Add(time.Hour)
					if err := os.Chtimes(path, changed, changed); err != nil {
						t.Fatal(err)
					}
				case "type", "symlink":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if change == "type" {
						err = os.Mkdir(path, 0755)
					} else {
						err = os.Symlink("elsewhere", path)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				injected := errors.New("injected before commit")
				fail := func() error { return injected }
				if replacement {
					next := filepath.Join(tmp, "next")
					if err := makeDestDir(next, map[string]string{"usr/bin/new": "new"}); err != nil {
						t.Fatal(err)
					}
					cfg.Version = "2"
					cfg.ReplacedVDBPath = filepath.Join(vdb, "cat/pkg-1")
					if change == "rollback" {
						cfg.BeforeCommit = fail
					}
					err = Merge(context.Background(), next, cfg)
				} else {
					unmerge := UnmergeConfig{RootDir: root, VDBDir: vdb, PackagePath: filepath.Join(vdb, "cat/pkg-1"), JournalDir: journals}
					if change == "rollback" {
						unmerge.BeforeCommit = fail
					}
					err = UnmergeWithConfig(context.Background(), unmerge)
				}
				if change == "rollback" {
					if !errors.Is(err, injected) {
						t.Fatalf("rollback: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				_, err = os.Lstat(path)
				if change == "unchanged" {
					if !os.IsNotExist(err) {
						t.Fatalf("unchanged file retained: %v", err)
					}
				} else if err != nil {
					t.Fatalf("user object lost: %v", err)
				}
				if change == "rollback" {
					if _, err := os.Stat(filepath.Join(vdb, "cat/pkg-1/CONTENTS")); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestRemovalSymlinkTargetContract(t *testing.T) {
	for _, contents := range []string{"sym /link -> original 123", "sym /link -> original 919c8b643b7133116b02fc0d9bb7df3f 123"} {
		entries, err := parseContents(contents)
		if err != nil || len(entries) != 1 {
			t.Fatalf("parse: %v %v", entries, err)
		}
		if entries[0].LinkTarget != "original" {
			t.Fatalf("link target: %q", entries[0].LinkTarget)
		}
	}
}
