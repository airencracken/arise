package merge

import (
	"compress/bzip2"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/oplock"
	"golang.org/x/sys/unix"
)

func makeDestDir(base string, entries map[string]string) error {
	for path, content := range entries {
		fullPath := filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

func TestMergeUsesSuppliedActiveJournal(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	image := filepath.Join(tmp, "image")
	vdb := filepath.Join(root, "var", "db", "pkg")
	for _, directory := range []string{root, image} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(image, "payload"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	operation, err := journal.Begin(filepath.Join(tmp, "journals"), root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", Journal: operation}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatalf("merge with supplied journal: %v", err)
	}
	if operation.Status() != "committed" {
		t.Fatalf("supplied journal status=%q", operation.Status())
	}
	if got, err := os.ReadFile(filepath.Join(root, "payload")); err != nil || string(got) != "new" {
		t.Fatalf("payload=%q err=%v", got, err)
	}
}

func TestMergeRejectsSuppliedJournalForDifferentRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	other := filepath.Join(tmp, "other")
	image := filepath.Join(tmp, "image")
	for _, directory := range []string{root, other, image} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	operation, err := journal.Begin(filepath.Join(tmp, "journals"), other)
	if err != nil {
		t.Fatal(err)
	}
	err = Merge(context.Background(), image, MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1", Journal: operation})
	if err == nil || !strings.Contains(err.Error(), "does not match active target root") {
		t.Fatalf("mismatched supplied journal error=%v", err)
	}
	if operation.Status() != "active" {
		t.Fatalf("rejected journal status=%q", operation.Status())
	}
}

func TestTransactionalProcessDeathRecovery(t *testing.T) {
	if mode := os.Getenv("ARISE_MERGE_DEATH_HELPER"); mode != "" {
		root, image := os.Getenv("ARISE_TEST_ROOT"), os.Getenv("ARISE_TEST_IMAGE")
		vdb, journals, marker := os.Getenv("ARISE_TEST_VDB"), os.Getenv("ARISE_TEST_JOURNALS"), os.Getenv("ARISE_TEST_MARKER")
		pauseBeforeCommit := func() error {
			if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
				return err
			}
			select {}
		}
		switch mode {
		case "merge":
			_ = Merge(context.Background(), image, MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", JournalDir: journals, BeforeCommit: pauseBeforeCommit})
		case "merge-preimage":
			_ = Merge(context.Background(), image, MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", JournalDir: journals, AfterPreimageBatch: pauseBeforeCommit})
		case "merge-synced":
			_ = Merge(context.Background(), image, MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", JournalDir: journals, AfterPayloadSync: pauseBeforeCommit})
		case "unmerge":
			_ = UnmergeWithConfig(context.Background(), UnmergeConfig{RootDir: root, VDBDir: vdb, PackagePath: filepath.Join(vdb, "cat", "pkg-1"), JournalDir: journals, BeforeCommit: pauseBeforeCommit})
		}
		os.Exit(97)
	}

	runUntilMutationThenKill := func(mode, root, image, vdb, journals string) {
		t.Helper()
		marker := filepath.Join(filepath.Dir(journals), mode+"-ready")
		command := exec.Command(os.Args[0], "-test.run=^TestTransactionalProcessDeathRecovery$")
		command.Env = append(os.Environ(),
			"ARISE_MERGE_DEATH_HELPER="+mode, "ARISE_TEST_ROOT="+root,
			"ARISE_TEST_IMAGE="+image, "ARISE_TEST_VDB="+vdb,
			"ARISE_TEST_JOURNALS="+journals, "ARISE_TEST_MARKER="+marker,
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				_ = command.Process.Kill()
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				_ = command.Process.Kill()
				t.Fatalf("%s child did not reach the pre-commit boundary", mode)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := command.Wait(); err == nil {
			t.Fatalf("%s child unexpectedly exited successfully", mode)
		}
	}

	for _, boundary := range []string{"merge-preimage", "merge", "merge-synced"} {
		boundary := boundary
		t.Run(boundary+" retry recovers killed transaction", func(t *testing.T) {
			tmp := t.TempDir()
			root, image := filepath.Join(tmp, "root"), filepath.Join(tmp, "image")
			vdb, journals := filepath.Join(root, "var", "db", "pkg"), filepath.Join(tmp, "journals")
			if err := makeDestDir(image, map[string]string{"usr/share/pkg/payload": "new"}); err != nil {
				t.Fatal(err)
			}
			runUntilMutationThenKill(boundary, root, image, vdb, journals)
			if err := Merge(context.Background(), image, MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", JournalDir: journals}); err != nil {
				t.Fatalf("merge retry: %v", err)
			}
			for _, path := range []string{filepath.Join(root, "usr", "share", "pkg", "payload"), filepath.Join(vdb, "cat", "pkg-1", "CONTENTS")} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("recovered merge result %s: %v", path, err)
				}
			}
			assertRecoveryJournalPair(t, journals)
		})
	}

	t.Run("unmerge retry recovers killed transaction", func(t *testing.T) {
		tmp := t.TempDir()
		root, image := filepath.Join(tmp, "root"), filepath.Join(tmp, "image")
		vdb := filepath.Join(root, "var", "db", "pkg")
		if err := makeDestDir(image, map[string]string{"usr/share/pkg/payload": "installed"}); err != nil {
			t.Fatal(err)
		}
		if err := Merge(context.Background(), image, MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "install-journals")}); err != nil {
			t.Fatal(err)
		}
		journals := filepath.Join(tmp, "unmerge-journals")
		runUntilMutationThenKill("unmerge", root, image, vdb, journals)
		packagePath := filepath.Join(vdb, "cat", "pkg-1")
		if err := UnmergeAt(context.Background(), root, vdb, packagePath, journals); err != nil {
			t.Fatalf("unmerge retry: %v", err)
		}
		for _, path := range []string{filepath.Join(root, "usr", "share", "pkg", "payload"), packagePath} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("recovered unmerge retained %s: %v", path, err)
			}
		}
		assertRecoveryJournalPair(t, journals)
	})
}

func assertRecoveryJournalPair(t *testing.T, directory string) {
	t.Helper()
	summaries, err := journal.List(directory)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(map[string]int)
	for _, summary := range summaries {
		statuses[summary.Status]++
	}
	if len(summaries) != 2 || statuses["rolled-back"] != 1 || statuses["committed"] != 1 {
		t.Fatalf("recovery journals = %#v", summaries)
	}
}

func TestValidateLiveNewInstallTargetsIsStrictlyAdditive(t *testing.T) {
	base := t.TempDir()
	image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
	for _, directory := range []string{filepath.Join(image, "usr", "bin"), filepath.Join(root, "usr", "bin")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	imageFile := filepath.Join(image, "usr", "bin", "canary")
	if err := os.WriteFile(imageFile, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveNewInstallTargets(image, root); err != nil {
		t.Fatalf("additive image rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "canary"), []byte("local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveNewInstallTargets(image, root); err == nil {
		t.Fatal("existing live target accepted")
	}
}

func TestValidateLiveReplacementTargetsRequiresOldContentsOwnership(t *testing.T) {
	base := t.TempDir()
	image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
	vdb := filepath.Join(root, "var", "db", "pkg", "cat", "pkg-1")
	for _, directory := range []string{filepath.Join(image, "usr", "bin"), filepath.Join(root, "usr", "bin"), vdb} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(image, "usr", "bin", "owned"), filepath.Join(root, "usr", "bin", "owned")} {
		if err := os.WriteFile(path, []byte("payload"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte("obj /usr/bin/owned md5 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err != nil {
		t.Fatalf("owned target rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(image, "usr", "bin", "local"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "local"), []byte("local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err == nil {
		t.Fatal("unowned replacement target accepted")
	}
}

func TestValidateLiveReplacementTargetsAcceptsOwnedPathContainingSpaces(t *testing.T) {
	base := t.TempDir()
	image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
	relative := filepath.Join("usr", "share", "cmake", "Help", "generator", "Borland Makefiles.rst")
	vdb := filepath.Join(root, "var", "db", "pkg", "dev-build", "cmake-4.2.4")
	for _, directory := range []string{filepath.Dir(filepath.Join(image, relative)), filepath.Dir(filepath.Join(root, relative)), vdb} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(image, relative), filepath.Join(root, relative)} {
		if err := os.WriteFile(path, []byte("generator help"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	contents := "obj /usr/share/cmake/Help/generator/Borland Makefiles.rst db933e4883648d1eaf6f223f84178a0d 1774614344\n"
	if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err != nil {
		t.Fatalf("owned path containing spaces rejected: %v", err)
	}
}

func TestValidateLiveReplacementTargetsAllowsMarkedGeneratedInfoIndex(t *testing.T) {
	base := t.TempDir()
	image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
	infoRelative := filepath.Join("usr", "share", "pkg-1", "info")
	vdb := filepath.Join(root, "var", "db", "pkg", "cat", "pkg-1")
	for _, directory := range []string{filepath.Join(image, infoRelative), filepath.Join(root, infoRelative), vdb} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(image, infoRelative, "dir"),
		filepath.Join(root, infoRelative, "dir"),
		filepath.Join(image, infoRelative, ".keepinfodir"),
	} {
		if err := os.WriteFile(path, []byte("generated info index\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte("dir /usr/share/pkg-1/info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err != nil {
		t.Fatalf("marked generated Info index rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(image, infoRelative, ".keepinfodir")); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err == nil {
		t.Fatal("unmarked generated Info index accepted")
	}
}

func TestValidateLiveReplacementTargetsAllowsCompressedGeneratedInfoIndexes(t *testing.T) {
	for _, name := range []string{"dir.gz", "dir.bz2", "dir.xz", "dir.zst"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
			infoRelative := filepath.Join("usr", "share", "info")
			vdb := filepath.Join(root, "var", "db", "pkg", "cat", "pkg-1")
			for _, directory := range []string{filepath.Join(image, infoRelative), filepath.Join(root, infoRelative), vdb} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{
				filepath.Join(image, infoRelative, name),
				filepath.Join(root, infoRelative, name),
				filepath.Join(image, infoRelative, ".keepinfodir"),
			} {
				if err := os.WriteFile(path, []byte("generated info index\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte("dir /usr/share/info\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validateLiveReplacementTargets(image, root, vdb); err != nil {
				t.Fatalf("marked compressed Info index rejected: %v", err)
			}
		})
	}
}

func TestValidateLiveReplacementTargetsRejectsCanonicalGlobalInfoIndexWithoutMarker(t *testing.T) {
	for _, name := range []string{"dir", "dir.gz", "dir.bz2", "dir.xz", "dir.zst"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
			infoRelative := filepath.Join("usr", "share", "info")
			vdb := filepath.Join(root, "var", "db", "pkg", "cat", "pkg-1")
			for _, directory := range []string{filepath.Join(image, infoRelative), filepath.Join(root, infoRelative), vdb} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{filepath.Join(image, infoRelative, name), filepath.Join(root, infoRelative, name)} {
				if err := os.WriteFile(path, []byte("generated info index\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte("dir /usr/share/info\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validateLiveReplacementTargets(image, root, vdb); err == nil {
				t.Fatal("canonical Info index escaped image finalization and was accepted")
			}
		})
	}
}

func TestMerge_BasicFiles(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/hello":            "#!/bin/sh\necho hello\n",
		"usr/share/doc/readme.txt": "Hello world\n",
		"etc/conf.d/test.conf":     "key=value\n",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "app-shells",
		Package:  "testpkg",
		Version:  "1.0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	checkFile := func(rel, expectedContent string) {
		path := filepath.Join(rootDir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", rel, err)
			return
		}
		if string(data) != expectedContent {
			t.Errorf("content mismatch for %s: got %q, want %q", rel, string(data), expectedContent)
		}
	}

	checkFile("usr/bin/hello", "#!/bin/sh\necho hello\n")
	checkFile("usr/share/doc/readme.txt", "Hello world\n")
	checkFile("etc/conf.d/test.conf", "key=value\n")

	contentsPath := filepath.Join(vdbDir, "app-shells", "testpkg-1.0", "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}

	contents := string(contentsData)
	for _, expected := range []string{
		"obj /",
		"usr/bin/hello",
		"usr/share/doc/readme.txt",
		"etc/conf.d/test.conf",
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("CONTENTS missing expected entry %q, contents:\n%s", expected, contents)
		}
	}

	envPath := filepath.Join(vdbDir, "app-shells", "testpkg-1.0", ".environment")
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("environment file not found: %v", err)
	}
}

func TestAbsoluteImageSymlinkTargetDependsOnEAPI(t *testing.T) {
	for _, test := range []struct {
		eapi       string
		wantStaged bool
	}{{"8", false}, {"9", true}} {
		t.Run("EAPI-"+test.eapi, func(t *testing.T) {
			tmp := t.TempDir()
			dest := filepath.Join(tmp, "image")
			root := filepath.Join(tmp, "root")
			vdb := filepath.Join(root, "var", "db", "pkg")
			if err := os.MkdirAll(filepath.Join(dest, "usr", "lib"), 0o755); err != nil {
				t.Fatal(err)
			}
			stagedTarget := filepath.Join(dest, "usr", "lib", "target")
			if err := os.Symlink(stagedTarget, filepath.Join(dest, "usr", "lib", "link")); err != nil {
				t.Fatal(err)
			}
			cfg := MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "1", VDBMetadata: map[string]string{"EAPI": test.eapi}}
			if err := Merge(context.Background(), dest, cfg); err != nil {
				t.Fatal(err)
			}
			got, err := os.Readlink(filepath.Join(root, "usr", "lib", "link"))
			if err != nil {
				t.Fatal(err)
			}
			want := "/usr/lib/target"
			if test.wantStaged {
				want = stagedTarget
			}
			if got != want {
				t.Fatalf("target=%q want=%q", got, want)
			}
		})
	}
}

func TestMerge_SymlinksAndDirs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(filepath.Join(destDir, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr/lib", "libfoo.so.1"), []byte("lib"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libfoo.so.1", filepath.Join(destDir, "usr/lib", "libfoo.so")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destDir, "var/lib", "emptydir"), 0755); err != nil {
		t.Fatal(err)
	}
	directoryTime := time.Unix(1_700_000_200, 0)
	if err := os.Chmod(filepath.Join(destDir, "var", "lib", "emptydir"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(destDir, "var", "lib", "emptydir"), directoryTime, directoryTime); err != nil {
		t.Fatal(err)
	}
	symlinkTime := time.Unix(1_700_000_300, 0)
	times := []unix.Timespec{unix.NsecToTimespec(symlinkTime.UnixNano()), unix.NsecToTimespec(symlinkTime.UnixNano())}
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, filepath.Join(destDir, "usr/lib", "libfoo.so"), times, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "sys-libs",
		Package:  "testlib",
		Version:  "2.0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	linkPath := filepath.Join(rootDir, "usr/lib", "libfoo.so")
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Errorf("Readlink: %v", err)
	}
	if linkTarget != "libfoo.so.1" {
		t.Errorf("symlink target = %q, want %q", linkTarget, "libfoo.so.1")
	}

	dirPath := filepath.Join(rootDir, "var/lib", "emptydir")
	if fi, err := os.Stat(dirPath); err != nil || !fi.IsDir() {
		t.Errorf("directory %s not created correctly: err=%v, isDir=%v", dirPath, err, fi != nil && fi.IsDir())
	}
	dirInfo, err := os.Stat(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o750 || dirInfo.ModTime().Unix() != directoryTime.Unix() {
		t.Errorf("directory metadata mode=%v mtime=%v", dirInfo.Mode().Perm(), dirInfo.ModTime())
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.ModTime().Unix() != symlinkTime.Unix() {
		t.Errorf("symlink mtime=%v", linkInfo.ModTime())
	}

	contentsPath := filepath.Join(vdbDir, "sys-libs", "testlib-2.0", "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}
	contents := string(contentsData)
	if !strings.Contains(contents, "sym ") {
		t.Errorf("CONTENTS missing sym entry:\n%s", contents)
	}
	if !strings.Contains(contents, "dir ") {
		t.Errorf("CONTENTS missing dir entry:\n%s", contents)
	}
}

func TestTransactionalMergePreservesHardlinksAndRegularFileTimestamp(t *testing.T) {
	tmp := t.TempDir()
	image := filepath.Join(tmp, "image")
	first := filepath.Join(image, "usr", "bin", "first")
	second := filepath.Join(image, "usr", "bin", "second")
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("shared inode"), 0o751); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	wantTime := time.Unix(1_700_000_123, 0)
	if err := os.Chtimes(first, wantTime, wantTime); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "root")
	cfg := MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "links", Version: "1", JournalDir: filepath.Join(tmp, "journals")}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	installedFirst := filepath.Join(root, "usr", "bin", "first")
	installedSecond := filepath.Join(root, "usr", "bin", "second")
	firstInfo, err := os.Stat(installedFirst)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(installedSecond)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("installed files do not share an inode")
	}
	if firstInfo.Mode().Perm() != 0o751 {
		t.Fatalf("mode=%o", firstInfo.Mode().Perm())
	}
	if firstInfo.ModTime().Unix() != wantTime.Unix() {
		t.Fatalf("mtime=%d want=%d", firstInfo.ModTime().Unix(), wantTime.Unix())
	}
	sourceStat := mustStatT(t, first)
	installedStat := mustStatT(t, installedFirst)
	if installedStat.Uid != sourceStat.Uid || installedStat.Gid != sourceStat.Gid {
		t.Fatalf("ownership=%d:%d want=%d:%d", installedStat.Uid, installedStat.Gid, sourceStat.Uid, sourceStat.Gid)
	}
}

func TestTransactionalMergePreservesExtendedAttributes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux xattr transport")
	}
	tmp := t.TempDir()
	image := filepath.Join(tmp, "image")
	source := filepath.Join(image, "usr", "share", "xattr-file")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(source, "user.arise-test", []byte("metadata"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("temporary filesystem lacks xattrs: %v", err)
		}
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "root")
	cfg := MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "xattr", Version: "1", JournalDir: filepath.Join(tmp, "journals")}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	value := make([]byte, 32)
	size, err := unix.Getxattr(filepath.Join(root, "usr", "share", "xattr-file"), "user.arise-test", value)
	if err != nil {
		t.Fatal(err)
	}
	if string(value[:size]) != "metadata" {
		t.Fatalf("xattr=%q", value[:size])
	}
}

func TestTransactionalMergeHandlesSafeTypeTransitionsAndRejectsNonEmptyDirectory(t *testing.T) {
	tmp := t.TempDir()
	image := filepath.Join(tmp, "image")
	root := filepath.Join(tmp, "root")
	if err := os.MkdirAll(filepath.Join(image, "types", "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "types", "regular"), []byte("new file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular", filepath.Join(image, "types", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "types", "blocked"), []byte("must not install"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "types", "regular"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "types", "directory"), []byte("old file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "types", "link"), []byte("old file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "types", "blocked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "types", "blocked", "local"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "types", Version: "1", JournalDir: filepath.Join(tmp, "journals")}
	err := Merge(context.Background(), image, cfg)
	if err == nil || !strings.Contains(err.Error(), "refusing to replace non-empty local directory") {
		t.Fatalf("Merge error=%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "types", "directory")); err != nil || string(data) != "old file" {
		t.Fatalf("file-to-directory rollback=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "types", "link")); err != nil || string(data) != "old file" {
		t.Fatalf("file-to-symlink rollback=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "types", "blocked", "local")); err != nil || string(data) != "keep" {
		t.Fatalf("non-empty directory changed=%q err=%v", data, err)
	}
	if err := os.Remove(filepath.Join(image, "types", "blocked")); err != nil {
		t.Fatal(err)
	}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "types", "directory")); err != nil || !info.IsDir() {
		t.Fatalf("file-to-directory result info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "types", "regular")); err != nil || string(data) != "new file" {
		t.Fatalf("directory-to-file result=%q err=%v", data, err)
	}
	if target, err := os.Readlink(filepath.Join(root, "types", "link")); err != nil || target != "regular" {
		t.Fatalf("file-to-symlink result=%q err=%v", target, err)
	}
}

func mustStatT(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s has no syscall.Stat_t", path)
	}
	return stat
}

func TestTransactionalMergeRollsBackFilesystemAndVDBOnFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	journalDir := filepath.Join(tmp, "journals")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	if err := makeDestDir(destDir, map[string]string{"a/good": "new", "z/block/child": "never"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "z"), 0o755); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(rootDir, "z", "block")
	if err := os.WriteFile(blocked, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "1", JournalDir: journalDir, BeforeCommit: func() error { return fmt.Errorf("injected failure") }}
	if err := Merge(ctx, destDir, cfg); err == nil || !strings.Contains(err.Error(), "rolled back via") {
		t.Fatalf("Merge error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootDir, "a")); !os.IsNotExist(err) {
		t.Fatalf("early mutation survived rollback: %v", err)
	}
	data, err := os.ReadFile(blocked)
	if err != nil || string(data) != "before" {
		t.Fatalf("blocked preimage=%q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(vdbDir, "cat", "pkg-1")); !os.IsNotExist(err) {
		t.Fatalf("VDB survived rollback: %v", err)
	}
	operations, err := os.ReadDir(journalDir)
	if err != nil || len(operations) != 1 {
		t.Fatalf("journal operations=%v err=%v", operations, err)
	}
	reopened, err := journal.Open(filepath.Join(journalDir, operations[0].Name()))
	if err != nil || reopened.Status() != "rolled-back" {
		t.Fatalf("journal status=%v err=%v", reopened, err)
	}
}

func TestTransactionalMergeCommitsDurableJournal(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	journalDir := filepath.Join(tmp, "journals")
	if err := makeDestDir(destDir, map[string]string{"usr/bin/tool": "payload"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: filepath.Join(rootDir, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1", JournalDir: journalDir}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	operations, err := os.ReadDir(journalDir)
	if err != nil || len(operations) != 1 {
		t.Fatalf("journal operations=%v err=%v", operations, err)
	}
	reopened, err := journal.Open(filepath.Join(journalDir, operations[0].Name()))
	if err != nil || reopened.Status() != "committed" {
		t.Fatalf("journal status=%v err=%v", reopened, err)
	}
}

func TestValidateLiveReplacementCanonicalizesMergedUsrOwnership(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	image := filepath.Join(tmp, "image")
	vdb := filepath.Join(tmp, "vdb")
	if err := os.MkdirAll(filepath.Join(root, "usr", "lib", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(root, "usr", "lib", "systemd", "system", "cronie.service")
	if err := os.WriteFile(unit, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(image, map[string]string{"usr/lib/systemd/system/cronie.service": "new"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vdb, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "obj /lib/systemd/system/cronie.service deadbeef 0\n"
	if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err != nil {
		t.Fatalf("merged-/usr owner was rejected: %v", err)
	}
}

func TestReplacementRetainsMergedUsrAliasInstalledByNewImage(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	image := filepath.Join(tmp, "image")
	vdbRoot := filepath.Join(root, "var", "db", "pkg")
	oldVDB := filepath.Join(vdbRoot, "sys-process", "cronie-1")
	if err := os.MkdirAll(filepath.Join(root, "usr", "lib", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(root, "usr", "lib", "systemd", "system", "cronie.service")
	if err := os.WriteFile(unit, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(image, map[string]string{"usr/lib/systemd/system/cronie.service": "new"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldVDB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldVDB, "CONTENTS"), []byte("obj /lib/systemd/system/cronie.service deadbeef 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{
		RootDir: root, VdbDir: vdbRoot, Category: "sys-process", Package: "cronie", Version: "2",
		JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldVDB,
	}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(unit)
	if err != nil || string(data) != "new" {
		t.Fatalf("new merged-/usr unit=%q err=%v", data, err)
	}
	if _, err := os.Lstat(oldVDB); !os.IsNotExist(err) {
		t.Fatalf("old VDB survived replacement: %v", err)
	}
}

func TestTransactionalMergeCoalescesNewSubtreeJournal(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	journalDir := filepath.Join(tmp, "journals")
	parent := filepath.Join(rootDir, "usr", "src")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	files := 1000
	if raw := os.Getenv("ARISE_TEST_TREE_FILES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("invalid ARISE_TEST_TREE_FILES=%q", raw)
		}
		files = parsed
	}
	for index := 0; index < files; index++ {
		path := filepath.Join(destDir, "usr", "src", "linux-new", "drivers", fmt.Sprintf("driver-%04d.c", index))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := MergeConfig{
		RootDir: rootDir, VdbDir: filepath.Join(rootDir, "var", "db", "pkg"),
		Category: "sys-kernel", Package: "sources", Version: "1", JournalDir: journalDir,
	}
	commit := os.Getenv("ARISE_TEST_TREE_COMMIT") == "1"
	if !commit {
		cfg.BeforeCommit = func() error { return fmt.Errorf("injected after subtree install") }
	}
	mergeErr := Merge(context.Background(), destDir, cfg)
	if commit {
		if mergeErr != nil {
			t.Fatalf("committed Merge error=%v", mergeErr)
		}
		if _, err := os.Stat(filepath.Join(parent, "linux-new", "drivers", fmt.Sprintf("driver-%04d.c", files-1))); err != nil {
			t.Fatalf("committed subtree missing: %v", err)
		}
	} else {
		if mergeErr == nil || !strings.Contains(mergeErr.Error(), "rolled back via") {
			t.Fatalf("Merge error=%v", mergeErr)
		}
		if _, err := os.Lstat(filepath.Join(parent, "linux-new")); !os.IsNotExist(err) {
			t.Fatalf("coalesced subtree survived rollback: %v", err)
		}
	}
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		t.Fatalf("existing parent was not preserved: info=%v err=%v", info, err)
	}
	summaries, err := journal.List(journalDir)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("journal summaries=%v err=%v", summaries, err)
	}
	if summaries[0].Entries >= files/10 {
		t.Fatalf("journal entries=%d for %d-file new subtree; capture was not coalesced", summaries[0].Entries, files)
	}
}

func TestTransactionalReinstallRestoresExistingVDBOnFailure(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	vdbEntry := filepath.Join(vdbDir, "cat", "pkg-1")
	if err := os.MkdirAll(vdbEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vdbEntry, "CONTENTS"), []byte("old-vdb"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(destDir, map[string]string{"a/good": "new", "z/block/child": "never"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "z", "block"), []byte("obstacle"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "journals"), BeforeCommit: func() error { return fmt.Errorf("injected failure") }}
	if err := Merge(context.Background(), destDir, cfg); err == nil {
		t.Fatal("reinstall unexpectedly succeeded")
	}
	data, err := os.ReadFile(filepath.Join(vdbEntry, "CONTENTS"))
	if err != nil || string(data) != "old-vdb" {
		t.Fatalf("restored VDB=%q err=%v", data, err)
	}
}

func TestTransactionalUpgradeCommitsNewAndRemovesOldVDB(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	oldEntry := filepath.Join(vdbDir, "cat", "pkg-1")
	if err := os.MkdirAll(oldEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldEntry, "CONTENTS"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(destDir, map[string]string{"usr/bin/tool": "v2"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "2", JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldEntry}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(oldEntry); !os.IsNotExist(err) {
		t.Fatalf("old VDB remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vdbDir, "cat", "pkg-2", "CONTENTS")); err != nil {
		t.Fatalf("new VDB missing: %v", err)
	}
}

func TestTransactionalMergeRejectsForeignOwnerBeforeMutation(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	journalDir := filepath.Join(tmp, "journals")
	if err := makeDestDir(destDir, map[string]string{"usr/bin/shared": "new"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(rootDir, "usr", "bin", "shared")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("owned"), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(vdbDir, "other", "owner-1")
	if err := os.MkdirAll(owner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "CONTENTS"), []byte("obj /usr/bin/shared md5 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "1", JournalDir: journalDir}
	err := Merge(context.Background(), destDir, cfg)
	if err == nil || !strings.Contains(err.Error(), "other/owner") {
		t.Fatalf("Merge error=%v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "owned" {
		t.Fatalf("foreign file mutated: %q err=%v", data, readErr)
	}
	if _, statErr := os.Stat(journalDir); !os.IsNotExist(statErr) {
		t.Fatalf("journal started before preflight: %v", statErr)
	}
}

func TestTransactionalUpgradeRemovesOnlyObsoleteUnsharedPayload(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	oldEntry := filepath.Join(vdbDir, "cat", "pkg-1")
	otherEntry := filepath.Join(vdbDir, "other", "owner-1")
	for _, directory := range []string{oldEntry, otherEntry, filepath.Join(rootDir, "usr", "bin")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"obsolete": "old", "shared": "shared", "retained": "old-retained"} {
		if err := os.WriteFile(filepath.Join(rootDir, "usr", "bin", name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldContents := "obj /usr/bin/obsolete md5 1\nobj /usr/bin/shared md5 1\nobj /usr/bin/retained md5 1\ndir /usr/bin\n"
	if err := os.WriteFile(filepath.Join(oldEntry, "CONTENTS"), []byte(oldContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherEntry, "CONTENTS"), []byte("obj /usr/bin/shared md5 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(destDir, map[string]string{"usr/bin/retained": "new-retained"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "2", JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldEntry}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(rootDir, "usr", "bin", "obsolete")); !os.IsNotExist(err) {
		t.Fatalf("obsolete payload remains: %v", err)
	}
	for name, want := range map[string]string{"shared": "shared", "retained": "new-retained"} {
		data, err := os.ReadFile(filepath.Join(rootDir, "usr", "bin", name))
		if err != nil || string(data) != want {
			t.Fatalf("%s=%q err=%v", name, data, err)
		}
	}
}

func TestTransactionalUpgradeRollsBackAfterPayloadCleanupFailure(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	oldEntry := filepath.Join(vdbDir, "cat", "pkg-1")
	if err := os.MkdirAll(oldEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPayload := filepath.Join(rootDir, "usr", "bin", "obsolete")
	if err := os.MkdirAll(filepath.Dir(oldPayload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPayload, []byte("old-payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldEntry, "CONTENTS"), []byte("obj /usr/bin/obsolete md5 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unsupported VDB objects fail closed during old-tree capture, after payload
	// cleanup has run, and force rollback across the replacement boundary.
	if err := syscall.Mkfifo(filepath.Join(oldEntry, "unsupported-fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(destDir, map[string]string{"usr/bin/new": "new-payload"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "2", JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldEntry}
	if err := Merge(context.Background(), destDir, cfg); err == nil || !strings.Contains(err.Error(), "rolled back via") {
		t.Fatalf("Merge error=%v", err)
	}
	data, err := os.ReadFile(oldPayload)
	if err != nil || string(data) != "old-payload" {
		t.Fatalf("old payload=%q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(rootDir, "usr", "bin", "new")); !os.IsNotExist(err) {
		t.Fatalf("new payload survived rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldEntry, "CONTENTS")); err != nil {
		t.Fatalf("old VDB missing after rollback: %v", err)
	}
}

func TestRequiredPreservedPathsInspectsELFWhenProviderMetadataMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF runtime library fixture requires Linux")
	}
	var source string
	for _, candidate := range []string{"/lib64/libc.so.6", "/lib/x86_64-linux-gnu/libc.so.6", "/usr/lib64/libc.so.6"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			source = candidate
			break
		}
	}
	if source == "" {
		t.Skip("no regular libc ELF fixture")
	}
	tmp := t.TempDir()
	root, vdb := filepath.Join(tmp, "root"), filepath.Join(tmp, "vdb")
	oldVDB := filepath.Join(vdb, "dev-libs", "provider-1")
	newVDB := filepath.Join(vdb, "dev-libs", "provider-2")
	consumerVDB := filepath.Join(vdb, "app-misc", "consumer-1")
	for _, directory := range []string{oldVDB, newVDB, consumerVDB, filepath.Join(root, "usr", "lib")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(root, "usr", "lib", "liblegacy-provider.so")
	if err := os.WriteFile(provider, data, 0o755); err != nil {
		t.Fatal(err)
	}
	soname := elfSONAME(provider)
	if soname == "" {
		t.Skip("ELF fixture has no SONAME")
	}
	if err := os.WriteFile(filepath.Join(consumerVDB, "NEEDED.ELF.2"), []byte("X86_64;/usr/bin/consumer;;;"+soname+";x86_64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []contentsEntry{{Type: "obj", Path: "/usr/lib/liblegacy-provider.so"}}
	got, err := requiredPreservedPaths(root, vdb, oldVDB, newVDB, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !got["/usr/lib/liblegacy-provider.so"] {
		t.Fatalf("actual ELF provider was not preserved without old NEEDED.ELF.2: %v", got)
	}
}

func TestTransactionalUpgradePreservesRequiredLibraryAndRegistry(t *testing.T) {
	tmp := t.TempDir()
	root, image := filepath.Join(tmp, "root"), filepath.Join(tmp, "image")
	vdb := filepath.Join(root, "var", "db", "pkg")
	oldVDB := filepath.Join(vdb, "dev-libs", "provider-1")
	consumerVDB := filepath.Join(vdb, "app-misc", "consumer-1")
	for _, directory := range []string{oldVDB, consumerVDB, filepath.Join(root, "usr", "lib"), image} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldLibrary := filepath.Join(root, "usr", "lib", "libarise.so.1.0")
	oldSONAME := filepath.Join(root, "usr", "lib", "libarise.so.1")
	oldDevelopment := filepath.Join(root, "usr", "lib", "libarise.so")
	if err := os.WriteFile(oldLibrary, []byte("old-library"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libarise.so.1.0", oldSONAME); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libarise.so.1.0", oldDevelopment); err != nil {
		t.Fatal(err)
	}
	oldContents := "obj /usr/lib/libarise.so.1.0 md5 1\nsym /usr/lib/libarise.so.1 -> libarise.so.1.0 1\nsym /usr/lib/libarise.so -> libarise.so.1.0 1\n"
	for name, value := range map[string]string{
		"CONTENTS": oldContents, "SLOT": "0\n", "COUNTER": "7\n",
		"NEEDED.ELF.2": "X86_64;/usr/lib/libarise.so.1.0;libarise.so.1;;;x86_64\n",
	} {
		if err := os.WriteFile(filepath.Join(oldVDB, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(consumerVDB, "CONTENTS"), []byte("obj /usr/bin/consumer md5 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumerVDB, "NEEDED.ELF.2"), []byte("X86_64;/usr/bin/consumer;;;libarise.so.1;x86_64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "0\n", "COUNTER": "8\n"} {
		if err := os.WriteFile(filepath.Join(consumerVDB, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := makeDestDir(image, map[string]string{"usr/lib/libarise.so.2.0": "new-library"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libarise.so.2.0", filepath.Join(image, "usr", "lib", "libarise.so")); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{
		RootDir: root, VdbDir: vdb, Category: "dev-libs", Package: "provider", Version: "2",
		JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldVDB, PreserveLibs: true,
		VDBMetadata: map[string]string{"SLOT": "0", "NEEDED.ELF.2": "X86_64;/usr/lib/libarise.so.2.0;libarise.so.2;;;x86_64"},
	}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oldLibrary, oldSONAME} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("required preserved library %s: %v", path, err)
		}
	}
	if target, err := os.Readlink(oldDevelopment); err != nil || target != "libarise.so.2.0" {
		t.Fatalf("development link did not select new ABI: target=%q err=%v", target, err)
	}
	registryPath := filepath.Join(root, "var", "lib", "portage", "preserved_libs_registry")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dev-libs/provider:0", "dev-libs/provider-2", "/usr/lib/libarise.so.1", "/usr/lib/libarise.so.1.0"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("preserved registry missing %q: %s", want, data)
		}
	}
	// Reproduce a registry left with an unversioned link that the new provider
	// has retargeted to its current ABI. Following this link during pruning must
	// neither retain the old ABI record nor delete the current link.
	var registry map[string][]json.RawMessage
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatal(err)
	}
	record := registry["dev-libs/provider:0"]
	var registered []string
	if err := json.Unmarshal(record[2], &registered); err != nil {
		t.Fatal(err)
	}
	registered = append(registered, "/usr/lib/libarise.so")
	record[2], _ = json.Marshal(registered)
	registry["dev-libs/provider:0"] = record
	data, _ = json.MarshalIndent(registry, "", "\t")
	if err := os.WriteFile(registryPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(oldVDB); !os.IsNotExist(err) {
		t.Fatalf("old provider VDB retained: %v", err)
	}
	// Portage records preserved paths in the current provider's CONTENTS. The
	// final-consumer cleanup must remove that ownership as well as the files.
	providerContents := filepath.Join(vdb, "dev-libs", "provider-2", "CONTENTS")
	currentContents, err := os.ReadFile(providerContents)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"/usr/lib/libarise.so.1", "/usr/lib/libarise.so.1.0"} {
		if !strings.Contains(string(currentContents), preserved) {
			t.Fatalf("new provider CONTENTS did not take preserved ownership of %s: %s", preserved, currentContents)
		}
	}
	consumerImage := filepath.Join(tmp, "consumer-image")
	if err := makeDestDir(consumerImage, map[string]string{"usr/bin/consumer": "rebuilt-without-old-soname"}); err != nil {
		t.Fatal(err)
	}
	consumerCfg := MergeConfig{
		RootDir: root, VdbDir: vdb, Category: "app-misc", Package: "consumer", Version: "2",
		JournalDir: filepath.Join(tmp, "consumer-journals"), ReplacedVDBPath: consumerVDB,
		PreserveLibs: true, VDBMetadata: map[string]string{
			"SLOT":         "0",
			"NEEDED.ELF.2": "X86_64;/usr/bin/consumer;;;libarise.so;x86_64",
		},
		BeforeCommit: func() error { return fmt.Errorf("injected consumer finalization failure") },
	}
	if err := Merge(context.Background(), consumerImage, consumerCfg); err == nil || !strings.Contains(err.Error(), "injected consumer finalization failure") {
		t.Fatalf("failed consumer rebuild = %v", err)
	}
	for _, path := range []string{oldLibrary, oldSONAME, filepath.Join(consumerVDB, "CONTENTS")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("rollback did not restore %s: %v", path, err)
		}
	}
	data, err = os.ReadFile(registryPath)
	if err != nil || !strings.Contains(string(data), "dev-libs/provider:0") {
		t.Fatalf("rollback registry = %q, %v", data, err)
	}
	consumerCfg.BeforeCommit = nil
	consumerCfg.JournalDir = filepath.Join(tmp, "consumer-retry-journals")
	if err := Merge(context.Background(), consumerImage, consumerCfg); err != nil {
		t.Fatalf("consumer rebuild: %v", err)
	}
	for _, path := range []string{oldLibrary, oldSONAME} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unused preserved library retained %s: %v", path, err)
		}
	}
	if target, err := os.Readlink(oldDevelopment); err != nil || target != "libarise.so.2.0" {
		t.Fatalf("current development link was pruned: target=%q err=%v", target, err)
	}
	data, err = os.ReadFile(registryPath)
	if err != nil || strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("pruned preserved registry = %q, %v", data, err)
	}
	data, err = os.ReadFile(providerContents)
	if err != nil || strings.Contains(string(data), "libarise.so.1") {
		t.Fatalf("provider CONTENTS retained preserved ownership = %q, %v", data, err)
	}
}

func TestTransactionalMergeWritesValidatedVDBMetadata(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	if err := makeDestDir(destDir, map[string]string{"usr/bin/tool": "payload"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{
		RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "journals"),
		VDBMetadata: map[string]string{"CATEGORY": "cat", "PF": "pkg-1", "EAPI": "8", "SLOT": "0", "repository": "test", "pkg-1.ebuild": "EAPI=8\n"},
		Environment: []byte("export CATEGORY='cat'\nexport PF='pkg-1'\n"),
	}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(vdbDir, "cat", "pkg-1")
	for name, want := range cfg.VDBMetadata {
		data, err := os.ReadFile(filepath.Join(entry, name))
		if err != nil || strings.TrimSuffix(string(data), "\n") != strings.TrimSuffix(want, "\n") {
			t.Fatalf("%s=%q err=%v", name, data, err)
		}
	}
	for _, name := range []string{"BUILD_TIME", "COUNTER"} {
		data, err := os.ReadFile(filepath.Join(entry, name))
		if err != nil || strings.TrimSpace(string(data)) == "" {
			t.Fatalf("%s=%q err=%v", name, data, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(rootDir, "var", "cache", "edb", "counter")); err != nil || string(data) != "1" {
		t.Fatalf("global counter=%q err=%v", data, err)
	}
	compressed, err := os.Open(filepath.Join(entry, "environment.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := io.ReadAll(bzip2.NewReader(compressed))
	compressed.Close()
	if err != nil || string(environment) != string(cfg.Environment) {
		t.Fatalf("environment=%q err=%v", environment, err)
	}
}

func TestTransactionalMergeUnsafeVDBMetadataRollsBack(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	if err := makeDestDir(destDir, map[string]string{"usr/bin/tool": "payload"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: filepath.Join(rootDir, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "journals"), VDBMetadata: map[string]string{"../escape": "bad"}}
	if err := Merge(context.Background(), destDir, cfg); err == nil || !strings.Contains(err.Error(), "rolled back via") {
		t.Fatalf("Merge error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootDir, "usr", "bin", "tool")); !os.IsNotExist(err) {
		t.Fatalf("payload survived metadata failure: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootDir, "var", "cache", "edb", "counter")); !os.IsNotExist(err) {
		t.Fatalf("global counter survived rollback: %v", err)
	}
}

func TestTransactionalMergeCounterIncrementsAndRestoresPreviousValue(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	counterPath := filepath.Join(root, "var", "cache", "edb", "counter")
	if err := os.MkdirAll(filepath.Dir(counterPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(counterPath, []byte("41"), 0o644); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(tmp, "image")
	if err := makeDestDir(image, map[string]string{"usr/bin/tool": "payload"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "journals"), BeforeCommit: func() error { return fmt.Errorf("injected") }}
	if err := Merge(context.Background(), image, cfg); err == nil {
		t.Fatal("expected injected failure")
	}
	if data, err := os.ReadFile(counterPath); err != nil || string(data) != "41" {
		t.Fatalf("counter after rollback=%q err=%v", data, err)
	}
	cfg.BeforeCommit = nil
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(counterPath); err != nil || string(data) != "42" {
		t.Fatalf("global counter after commit=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(cfg.VdbPath(), "COUNTER")); err != nil || strings.TrimSpace(string(data)) != "42" {
		t.Fatalf("package counter=%q err=%v", data, err)
	}
}

func TestTransactionalReplacementLifecycleOrdering(t *testing.T) {
	tmp := t.TempDir()
	destDir, rootDir := filepath.Join(tmp, "dest"), filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	oldEntry := filepath.Join(vdbDir, "cat", "pkg-1")
	if err := os.MkdirAll(oldEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldEntry, "CONTENTS"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(destDir, map[string]string{"usr/bin/tool": "v2"}); err != nil {
		t.Fatal(err)
	}
	var order []string
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "2", JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldEntry,
		BeforeReplacementRemoval: func() error { order = append(order, "pkg_prerm"); return nil },
		AfterReplacementRemoval:  func() error { order = append(order, "pkg_postrm"); return nil },
		AfterCommit:              func() error { order = append(order, "pkg_postinst"); return nil },
	}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "pkg_prerm,pkg_postrm,pkg_postinst" {
		t.Fatalf("lifecycle order=%s", got)
	}
}

func TestPostCommitLifecycleFailureRetainsCommittedPackage(t *testing.T) {
	tmp := t.TempDir()
	image, root := filepath.Join(tmp, "image"), filepath.Join(tmp, "root")
	if err := makeDestDir(image, map[string]string{"usr/bin/tool": "payload"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{
		RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1",
		JournalDir: filepath.Join(tmp, "journals"), AfterCommit: func() error { return fmt.Errorf("injected postinst failure") },
	}
	err := Merge(context.Background(), image, cfg)
	var committed *PostCommitError
	if !errors.As(err, &committed) {
		t.Fatalf("error=%v, want PostCommitError", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(root, "usr", "bin", "tool")); readErr != nil || string(data) != "payload" {
		t.Fatalf("committed payload=%q err=%v", data, readErr)
	}
	if _, statErr := os.Stat(cfg.VdbPath()); statErr != nil {
		t.Fatalf("committed VDB missing: %v", statErr)
	}
	summaries, listErr := journal.List(cfg.JournalDir)
	if listErr != nil || len(summaries) != 1 || summaries[0].Status != "committed" {
		t.Fatalf("journal summaries=%#v err=%v", summaries, listErr)
	}
}

func TestTransactionalPostRemovalFailureRollsBackReplacement(t *testing.T) {
	tmp := t.TempDir()
	destDir, rootDir := filepath.Join(tmp, "dest"), filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	oldEntry, oldPayload := filepath.Join(vdbDir, "cat", "pkg-1"), filepath.Join(rootDir, "usr", "bin", "old")
	for _, directory := range []string{oldEntry, filepath.Dir(oldPayload)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(oldPayload, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldEntry, "CONTENTS"), []byte("obj /usr/bin/old md5 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeDestDir(destDir, map[string]string{"usr/bin/new": "new"}); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "pkg", Version: "2", JournalDir: filepath.Join(tmp, "journals"), ReplacedVDBPath: oldEntry, AfterReplacementRemoval: func() error { return fmt.Errorf("injected pkg_postrm failure") }}
	if err := Merge(context.Background(), destDir, cfg); err == nil || !strings.Contains(err.Error(), "rolled back via") {
		t.Fatalf("Merge error=%v", err)
	}
	data, err := os.ReadFile(oldPayload)
	if err != nil || string(data) != "old" {
		t.Fatalf("old payload=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(oldEntry, "CONTENTS")); err != nil {
		t.Fatalf("old VDB missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootDir, "usr", "bin", "new")); !os.IsNotExist(err) {
		t.Fatalf("new payload remains: %v", err)
	}
}

func TestTransactionalMergeConfigProtectAndMask(t *testing.T) {
	tmp := t.TempDir()
	destDir, rootDir := filepath.Join(tmp, "dest"), filepath.Join(tmp, "root")
	if err := makeDestDir(destDir, map[string]string{"etc/app.conf": "new", "etc/masked/value": "new-masked"}); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{"etc/app.conf": "local", "etc/masked/value": "old-masked"} {
		target := filepath.Join(rootDir, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: filepath.Join(rootDir, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "journals"), ConfigProtect: []string{"/etc"}, ConfigProtectMask: []string{"/etc/masked"}}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"etc/app.conf": "local", "etc/._cfg0000_app.conf": "new", "etc/masked/value": "new-masked"} {
		data, err := os.ReadFile(filepath.Join(rootDir, path))
		if err != nil || string(data) != want {
			t.Fatalf("%s=%q err=%v", path, data, err)
		}
	}
}

func TestTransactionalMergeConfigProtectDoesNotReadSymlinkToDirectory(t *testing.T) {
	tmp := t.TempDir()
	destDir, rootDir := filepath.Join(tmp, "dest"), filepath.Join(tmp, "root")
	for _, directory := range []string{
		filepath.Join(destDir, "etc", "bind"),
		filepath.Join(rootDir, "etc", "bind"),
		filepath.Join(rootDir, "var", "bind", "dyn"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const target = "../../var/bind/dyn"
	for _, path := range []string{filepath.Join(destDir, "etc", "bind", "dyn"), filepath.Join(rootDir, "etc", "bind", "dyn")} {
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: filepath.Join(rootDir, "var", "db", "pkg"), Category: "net-dns", Package: "bind", Version: "1", JournalDir: filepath.Join(tmp, "journals"), ConfigProtect: []string{"/etc"}}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(rootDir, "etc", "bind", "dyn")); err != nil || got != target {
		t.Fatalf("merged bind symlink=%q err=%v", got, err)
	}
}

func TestTransactionalMergeConfigProtectUsesNextCounter(t *testing.T) {
	tmp := t.TempDir()
	destDir, rootDir := filepath.Join(tmp, "dest"), filepath.Join(tmp, "root")
	if err := makeDestDir(destDir, map[string]string{"etc/app.conf": "new"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"app.conf": "local", "._cfg0000_app.conf": "pending"} {
		if err := os.WriteFile(filepath.Join(rootDir, "etc", name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := MergeConfig{RootDir: rootDir, VdbDir: filepath.Join(rootDir, "var", "db", "pkg"), Category: "cat", Package: "pkg", Version: "1", JournalDir: filepath.Join(tmp, "journals"), ConfigProtect: []string{"/etc"}}
	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(rootDir, "etc", "._cfg0001_app.conf"))
	if err != nil || string(data) != "new" {
		t.Fatalf("next cfg=%q err=%v", data, err)
	}
}

func TestLiveReplacementConfigProtectRetainsPendingUpdateLineage(t *testing.T) {
	base := t.TempDir()
	image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
	vdb := filepath.Join(root, "var", "db", "pkg", "dev-libs", "openssl-3.5.7")
	for _, directory := range []string{filepath.Join(image, "etc", "ssl", "misc"), filepath.Join(root, "etc", "ssl", "misc"), vdb} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{
		filepath.Join(image, "etc", "ssl", "misc", "CA.pl"):          "newer",
		filepath.Join(root, "etc", "ssl", "misc", "CA.pl"):           "local",
		filepath.Join(root, "etc", "ssl", "misc", "._cfg0000_CA.pl"): "new",
	} {
		if err := os.WriteFile(path, []byte(value), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	contents := "dir /etc/ssl/misc\nobj /etc/ssl/misc/._cfg0000_CA.pl d2bc7e8817584886c05bcddfc86f8f76 1784676332\n"
	if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargets(image, root, vdb); err == nil {
		t.Fatal("pending config update bypassed validation without CONFIG_PROTECT policy")
	}
	if err := validateLiveReplacementTargetsWithConfig(image, root, vdb, []string{"/etc"}, nil); err != nil {
		t.Fatalf("protected target with owned pending update rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "etc", "ssl", "misc", "._cfg0000_CA.pl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vdb, "CONTENTS"), []byte("dir /etc/ssl/misc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveReplacementTargetsWithConfig(image, root, vdb, []string{"/etc"}, nil); err == nil {
		t.Fatal("arbitrary unowned protected target accepted")
	}
}

func TestUnmerge(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/app":    "binary content",
		"usr/lib/libx.a": "archive",
		"etc/app.conf":   "config",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "app-misc",
		Package:  "app",
		Version:  "1.0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, rel := range []string{"usr/bin/app", "usr/lib/libx.a", "etc/app.conf"} {
		if _, err := os.Stat(filepath.Join(rootDir, rel)); err != nil {
			t.Fatalf("file %s should exist after merge: %v", rel, err)
		}
	}

	pkgPath := filepath.Join(vdbDir, "app-misc", "app-1.0")
	if err := UnmergeAt(ctx, rootDir, vdbDir, pkgPath, filepath.Join(tmp, "journals")); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}

	for _, rel := range []string{"usr/bin/app", "usr/lib/libx.a", "etc/app.conf"} {
		path := filepath.Join(rootDir, rel)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("file %s should not exist after unmerge: err=%v", rel, err)
		}
	}

	for _, rel := range []string{"usr/bin", "usr/lib", "usr", "etc"} {
		path := filepath.Join(rootDir, rel)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("empty directory %s should have been removed", rel)
		}
	}

	if _, err := os.Stat(pkgPath); !os.IsNotExist(err) {
		t.Errorf("VDB entry %s should not exist after unmerge", pkgPath)
	}
}

func TestTransactionalUnmergeRollsBackPayloadAndVDB(t *testing.T) {
	tmp := t.TempDir()
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	destDir := filepath.Join(tmp, "dest")
	if err := makeDestDir(destDir, map[string]string{"usr/bin/app": "payload"}); err != nil {
		t.Fatal(err)
	}
	mergeCfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "app", Version: "1", JournalDir: filepath.Join(tmp, "merge-journals")}
	if err := Merge(context.Background(), destDir, mergeCfg); err != nil {
		t.Fatal(err)
	}
	pkgPath := mergeCfg.VdbPath()
	err := UnmergeWithConfig(context.Background(), UnmergeConfig{
		RootDir: rootDir, VDBDir: vdbDir, PackagePath: pkgPath,
		JournalDir:   filepath.Join(tmp, "unmerge-journals"),
		BeforeCommit: func() error { return fmt.Errorf("injected finalization failure") },
	})
	if err == nil || !strings.Contains(err.Error(), "injected finalization failure") {
		t.Fatalf("UnmergeWithConfig error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(rootDir, "usr", "bin", "app"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("payload after rollback = %q, err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(pkgPath, "CONTENTS")); err != nil {
		t.Fatalf("VDB after rollback: %v", err)
	}
}

func TestUnmergeLifecycleOrderingAndPostCommitFailure(t *testing.T) {
	tmp := t.TempDir()
	root, image := filepath.Join(tmp, "root"), filepath.Join(tmp, "image")
	vdb := filepath.Join(root, "var", "db", "pkg")
	if err := makeDestDir(image, map[string]string{"usr/bin/app": "payload"}); err != nil {
		t.Fatal(err)
	}
	mergeCfg := MergeConfig{RootDir: root, VdbDir: vdb, Category: "cat", Package: "app", Version: "1", JournalDir: filepath.Join(tmp, "merge-journals")}
	if err := Merge(context.Background(), image, mergeCfg); err != nil {
		t.Fatal(err)
	}
	var order []string
	err := UnmergeWithConfig(context.Background(), UnmergeConfig{
		RootDir: root, VDBDir: vdb, PackagePath: mergeCfg.VdbPath(), JournalDir: filepath.Join(tmp, "unmerge-journals"),
		BeforeRemoval: func() error { order = append(order, "pkg_prerm"); return nil },
		AfterRemoval:  func() error { order = append(order, "pkg_postrm"); return nil },
		AfterCommit:   func() error { order = append(order, "committed"); return fmt.Errorf("postrm failed") },
	})
	var committed *PostCommitError
	if !errors.As(err, &committed) {
		t.Fatalf("error=%v, want PostCommitError", err)
	}
	if got := strings.Join(order, ","); got != "pkg_prerm,pkg_postrm,committed" {
		t.Fatalf("order=%s", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, "usr", "bin", "app")); !os.IsNotExist(statErr) {
		t.Fatalf("payload survived committed removal: %v", statErr)
	}
	if _, statErr := os.Stat(mergeCfg.VdbPath()); !os.IsNotExist(statErr) {
		t.Fatalf("VDB survived committed removal: %v", statErr)
	}
}

func TestUnmergeWithCallerHeldVDBLock(t *testing.T) {
	tmp := t.TempDir()
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")
	destDir := filepath.Join(tmp, "dest")
	if err := makeDestDir(destDir, map[string]string{"usr/bin/app": "payload"}); err != nil {
		t.Fatal(err)
	}
	mergeCfg := MergeConfig{RootDir: rootDir, VdbDir: vdbDir, Category: "cat", Package: "app", Version: "1", JournalDir: filepath.Join(tmp, "merge-journals")}
	if err := Merge(context.Background(), destDir, mergeCfg); err != nil {
		t.Fatal(err)
	}
	lock, err := oplock.TryAcquireVDB(vdbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := UnmergeWithConfig(context.Background(), UnmergeConfig{
		RootDir: rootDir, VDBDir: vdbDir, PackagePath: mergeCfg.VdbPath(),
		JournalDir: filepath.Join(tmp, "unmerge-journals"), VDBLockHeld: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mergeCfg.VdbPath()); !os.IsNotExist(err) {
		t.Fatalf("VDB remains: %v", err)
	}
}

func TestMerge_EmptyDestDir(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "app-empty",
		Package:  "noop",
		Version:  "0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "app-empty", "noop-0", "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}
	if len(contentsData) != 0 {
		t.Errorf("CONTENTS should be exactly zero bytes, got: %q", string(contentsData))
	}
}

func TestMerge_NonExistentRoot(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/foo": "hello",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	rootDir := filepath.Join(tmp, "nonexistent-root")
	vdbDir := filepath.Join(tmp, "vdb")

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "usr/bin/foo"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", string(data), "hello")
	}
}

func TestMerge_ContentsFormat(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(filepath.Join(destDir, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr/lib", "libx.so"), []byte("shared"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libx.so", filepath.Join(destDir, "usr/lib", "libx.so.1")); err != nil {
		t.Fatal(err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "cat", "pkg-1", "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}

	contents := string(data)
	lines := strings.Split(strings.TrimSpace(contents), "\n")

	hasDir := false
	hasObj := false
	hasSym := false

	for _, line := range lines {
		if strings.HasPrefix(line, "dir ") {
			hasDir = true
			fields := strings.Fields(line)
			if len(fields) < 2 {
				t.Errorf("dir line lacks path: %q", line)
			}
		}
		if strings.HasPrefix(line, "obj ") {
			hasObj = true
			fields := strings.Fields(line)
			if len(fields) < 4 {
				t.Errorf("obj line should have at least 4 fields (type path md5 mtime): %q", line)
			}
			if fields[2] == "" {
				t.Errorf("obj line has empty md5: %q", line)
			}
		}
		if strings.HasPrefix(line, "sym ") {
			hasSym = true
			if !strings.Contains(line, "->") {
				t.Errorf("sym line missing ->: %q", line)
			}
		}
	}

	if !hasDir {
		t.Error("CONTENTS missing dir entry")
	}
	if !hasObj {
		t.Error("CONTENTS missing obj entry")
	}
	if !hasSym {
		t.Error("CONTENTS missing sym entry")
	}
}

func TestUnmerge_Nonexistent(t *testing.T) {
	ctx := context.Background()
	err := Unmerge(ctx, "/tmp/nonexistent-path-arise-test-12345")
	if err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
}

func TestUnmerge_CorruptContents(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "cat", "pkg-1")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	corrupt := "garbage line\n\x00\x00\x00\nobj /usr/bin/x a1 0"
	if err := os.WriteFile(filepath.Join(pkgDir, "CONTENTS"), []byte(corrupt), 0644); err != nil {
		t.Fatal(err)
	}

	// should not panic, may error or succeed gracefully
	err := Unmerge(ctx, pkgDir)
	_ = err // acceptable either way
}

func TestMerge_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/x": "content",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  filepath.Join(tmp, "root"),
		VdbDir:   filepath.Join(tmp, "vdb"),
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	err := Merge(ctx, destDir, cfg)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestMerge_WithFilesWithSpecialPerms(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	fullPath := filepath.Join(destDir, "usr/bin", "setuid-binary")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("binary"), 04755); err != nil {
		t.Fatal(err)
	}

	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	fi, err := os.Stat(filepath.Join(rootDir, "usr/bin", "setuid-binary"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	got := fi.Mode()
	if got.Perm() != 0755 {
		t.Errorf("mode perms = %o, want %o", got.Perm(), 0755)
	}
	_ = got // mode bits may differ on nosuid mounts
}

func TestUnmerge_PreservesNonEmptyDirs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(rootDir, "var", "db", "pkg")

	if err := makeDestDir(destDir, map[string]string{
		"usr/share/pkg/file.txt": "data",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	// Create an extra file in the same dir that is not part of this package
	extraPath := filepath.Join(rootDir, "usr/share/pkg/extra.txt")
	if err := os.MkdirAll(filepath.Dir(extraPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraPath, []byte("not ours"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	pkgPath := filepath.Join(vdbDir, "cat", "pkg-1")
	if err := UnmergeAt(ctx, rootDir, vdbDir, pkgPath, filepath.Join(tmp, "journals")); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}

	// Our file should be gone
	if _, err := os.Stat(filepath.Join(rootDir, "usr/share/pkg/file.txt")); !os.IsNotExist(err) {
		t.Error("our file should have been removed")
	}
	// Extra file should remain
	if _, err := os.Stat(extraPath); err != nil {
		t.Error("extra file should still exist")
	}
	// Directory should still exist (not empty)
	if _, err := os.Stat(filepath.Join(rootDir, "usr/share/pkg")); err != nil {
		t.Error("directory with extra file should still exist")
	}
}

func TestParseContents(t *testing.T) {
	input := `obj /usr/bin/foo a1b2c3d4e5 1234567890
dir /usr/share/pkg
sym /usr/lib/libx.so -> libx.so.1 ffff 9876543210`

	entries, err := parseContents(input)
	if err != nil {
		t.Fatalf("parseContents: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Type != "obj" {
		t.Errorf("entry 0 type = %q, want obj", entries[0].Type)
	}
	if entries[0].MD5 != "a1b2c3d4e5" {
		t.Errorf("entry 0 md5 = %q", entries[0].MD5)
	}
	if entries[0].Mtime != 1234567890 {
		t.Errorf("entry 0 mtime = %d", entries[0].Mtime)
	}
	if entries[1].Type != "dir" {
		t.Errorf("entry 1 type = %q, want dir", entries[1].Type)
	}
	if entries[2].Type != "sym" {
		t.Errorf("entry 2 type = %q, want sym", entries[2].Type)
	}
}

func TestParseContents_Empty(t *testing.T) {
	entries, err := parseContents("")
	if err != nil {
		t.Fatalf("parseContents: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseContents_Adversarial(t *testing.T) {
	input := fmt.Sprintf("garbage\n\x00\x00\n%s\nshort",
		strings.Repeat("obj /x a1 0\n", 1000))

	entries, err := parseContents(input)
	if err != nil {
		t.Fatalf("parseContents should not error on adversarial input: %v", err)
	}
	if len(entries) < 1000 {
		t.Errorf("expected at least 1000 entries, got %d", len(entries))
	}
}

func TestUnmerge_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/x": "content",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	pkgPath := filepath.Join(vdbDir, "cat", "pkg-1")
	err := Unmerge(ctx, pkgPath)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestMerge_RecordsMtime(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	testPath := filepath.Join(destDir, "testfile")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(testPath, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "cat", "pkg-1", "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}

	contents := string(data)
	if !strings.Contains(contents, "obj ") {
		t.Errorf("CONTENTS missing obj entry: %s", contents)
	}

	lines := strings.Split(strings.TrimSpace(contents), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "obj ") {
			fields := strings.Fields(line)
			if len(fields) < 4 {
				t.Errorf("obj line too short: %q", line)
				continue
			}
			if fields[2] == "" {
				t.Error("md5 field is empty")
			}
			if fields[3] == "" || fields[3] == "0" {
				t.Errorf("mtime field is empty or zero: %q", fields[3])
			}
		}
	}
}
