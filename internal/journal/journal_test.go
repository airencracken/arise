package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestActiveJournalUsesAppendOnlyCaptureLog(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	operation, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	const captures = 200
	for index := 0; index < captures; index++ {
		if err := operation.Capture(filepath.Join(root, "created", fmt.Sprintf("%04d", index))); err != nil {
			t.Fatal(err)
		}
	}

	stateData, err := os.ReadFile(filepath.Join(operation.Dir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 0 {
		t.Fatalf("active state snapshot grew to %d entries", len(state.Entries))
	}
	logData, err := os.ReadFile(filepath.Join(operation.Dir(), entriesLogName))
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(logData, []byte{'\n'}); got != captures {
		t.Fatalf("capture log records = %d, want %d", got, captures)
	}
	reopened, err := Open(operation.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.state.Entries); got != captures {
		t.Fatalf("reopened entries = %d, want %d", got, captures)
	}
}

func TestRollbackInPlaceWritePreservesHardlinkTopology(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	operation, err := Begin(filepath.Join(tmp, "journals"), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Capture(first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := operation.Rollback(); err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("rollback broke the original hard-link relationship")
	}
	for _, path := range []string{first, second} {
		if got, err := os.ReadFile(path); err != nil || string(got) != "old" {
			t.Fatalf("%s=%q err=%v", path, got, err)
		}
	}
}

func TestOpenIgnoresTornFinalCaptureRecord(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	operation, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Capture(filepath.Join(root, "complete")); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(operation.Dir(), entriesLogName)
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"path":"torn`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(operation.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.state.Entries); got != 1 {
		t.Fatalf("reopened entries = %d, want 1", got)
	}
}

func BenchmarkCaptureAbsent(b *testing.B) {
	root, base := b.TempDir(), b.TempDir()
	operation, err := Begin(base, root)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := operation.Capture(filepath.Join(root, "created", fmt.Sprintf("%08d", index))); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCaptureAbsentTreeRollsBackAllCreatedDescendants(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	operation, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, "usr", "src", "linux-new")
	if err := operation.CaptureAbsentTree(tree); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, "drivers", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "drivers", "net", "driver.c"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := operation.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatalf("new subtree remains after rollback: %v", err)
	}
	reopened, err := Open(operation.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.state.Entries) != 1 || reopened.state.Entries[0].Kind != "absent-tree" {
		t.Fatalf("entries = %#v", reopened.state.Entries)
	}
}

func TestCaptureAbsentTreeRefusesExistingPath(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	tree := filepath.Join(root, "existing")
	if err := os.Mkdir(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	operation, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CaptureAbsentTree(tree); err == nil {
		t.Fatal("CaptureAbsentTree accepted an existing path")
	}
}

func TestCaptureBatchReopensAndRollsBackMixedPreimages(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	existing := filepath.Join(root, "existing")
	created := filepath.Join(root, "created")
	if err := os.WriteFile(existing, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	operation, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CaptureBatch([]string{existing, created, existing}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(operation.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.state.Entries); got != 2 {
		t.Fatalf("batch entries=%d, want 2", got)
	}
	if err := reopened.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "before" {
		t.Fatalf("existing preimage=%q err=%v", data, err)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("created path survived rollback: %v", err)
	}
}

func TestProcessDeathAtDurableBoundaries(t *testing.T) {
	if targetStage := os.Getenv("ARISE_JOURNAL_DEATH_STAGE"); targetStage != "" {
		root, base := os.Getenv("ARISE_TEST_ROOT"), os.Getenv("ARISE_TEST_JOURNALS")
		marker := os.Getenv("ARISE_TEST_MARKER")
		stop := func(stage string) {
			if stage != targetStage {
				return
			}
			if err := os.WriteFile(marker, []byte(stage), 0o600); err != nil {
				os.Exit(96)
			}
			select {}
		}
		operation, err := Begin(base, root)
		if err != nil {
			os.Exit(95)
		}
		stop("begin")
		existing, created := filepath.Join(root, "existing"), filepath.Join(root, "created")
		if err := operation.Capture(existing); err != nil {
			os.Exit(94)
		}
		stop("capture-existing")
		if err := os.WriteFile(existing, []byte("after"), 0o600); err != nil {
			os.Exit(93)
		}
		stop("mutate-existing")
		if err := operation.Capture(created); err != nil {
			os.Exit(92)
		}
		stop("capture-absent")
		if err := os.WriteFile(created, []byte("created"), 0o600); err != nil {
			os.Exit(91)
		}
		stop("mutate-absent")
		directory := filepath.Join(root, "directory")
		if err := operation.Capture(directory); err != nil {
			os.Exit(88)
		}
		stop("capture-directory")
		if err := os.Remove(directory); err != nil {
			os.Exit(87)
		}
		stop("mutate-directory")
		link := filepath.Join(root, "link")
		if err := operation.Capture(link); err != nil {
			os.Exit(86)
		}
		stop("capture-symlink")
		if err := os.Remove(link); err != nil {
			os.Exit(85)
		}
		if err := os.Symlink("created", link); err != nil {
			os.Exit(84)
		}
		stop("mutate-symlink")
		if err := operation.Commit(); err != nil {
			os.Exit(90)
		}
		stop("commit")
		os.Exit(89)
	}

	stages := []struct {
		name      string
		committed bool
	}{
		{name: "begin"},
		{name: "capture-existing"},
		{name: "mutate-existing"},
		{name: "capture-absent"},
		{name: "mutate-absent"},
		{name: "capture-directory"},
		{name: "mutate-directory"},
		{name: "capture-symlink"},
		{name: "mutate-symlink"},
		{name: "commit", committed: true},
	}
	for _, stage := range stages {
		stage := stage
		t.Run(stage.name, func(t *testing.T) {
			tmp := t.TempDir()
			root, base := filepath.Join(tmp, "root"), filepath.Join(tmp, "journals")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "existing"), []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "directory"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("existing", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(tmp, "ready")
			command := exec.Command(os.Args[0], "-test.run=^TestProcessDeathAtDurableBoundaries$")
			command.Env = append(os.Environ(),
				"ARISE_JOURNAL_DEATH_STAGE="+stage.name, "ARISE_TEST_ROOT="+root,
				"ARISE_TEST_JOURNALS="+base, "ARISE_TEST_MARKER="+marker,
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
					t.Fatalf("child did not reach %s", stage.name)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err == nil {
				t.Fatal("killed child exited successfully")
			}
			recovered, err := RecoverActive(base)
			if err != nil {
				t.Fatal(err)
			}
			if stage.committed {
				if len(recovered) != 0 {
					t.Fatalf("recovered committed journal: %v", recovered)
				}
				for path, want := range map[string]string{"existing": "after", "created": "created"} {
					data, err := os.ReadFile(filepath.Join(root, path))
					if err != nil || string(data) != want {
						t.Fatalf("committed %s=%q err=%v", path, data, err)
					}
				}
				if _, err := os.Lstat(filepath.Join(root, "directory")); !os.IsNotExist(err) {
					t.Fatalf("committed transaction retained directory: %v", err)
				}
				if target, err := os.Readlink(filepath.Join(root, "link")); err != nil || target != "created" {
					t.Fatalf("committed symlink=%q err=%v", target, err)
				}
				return
			}
			if len(recovered) != 1 {
				t.Fatalf("recovered journals = %v", recovered)
			}
			data, err := os.ReadFile(filepath.Join(root, "existing"))
			if err != nil || string(data) != "before" {
				t.Fatalf("restored existing=%q err=%v", data, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "created")); !os.IsNotExist(err) {
				t.Fatalf("rollback retained created path: %v", err)
			}
			if info, err := os.Stat(filepath.Join(root, "directory")); err != nil || !info.IsDir() {
				t.Fatalf("rollback directory=%v err=%v", info, err)
			}
			if target, err := os.Readlink(filepath.Join(root, "link")); err != nil || target != "existing" {
				t.Fatalf("rollback symlink=%q err=%v", target, err)
			}
		})
	}
}

func TestRollbackRestoresMetadataAndXattrs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metadata")
	wantTime := time.Unix(1_700_000_000, 123_456_789)
	if err := os.WriteFile(path, []byte("before"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "user.arise-journal", []byte("preserved"), 0); err != nil {
		if err == unix.ENOTSUP {
			t.Skip("xattrs unsupported")
		}
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, wantTime, wantTime); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Removexattr(path, "user.arise-journal"); err != nil {
		t.Fatal(err)
	}
	if err := j.Rollback(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("restored mode=%v", info.Mode())
	}
	if info.ModTime().UnixNano() != wantTime.UnixNano() {
		t.Fatalf("restored mtime=%v want=%v", info.ModTime(), wantTime)
	}
	value := make([]byte, 32)
	n, err := unix.Getxattr(path, "user.arise-journal", value)
	if err != nil || string(value[:n]) != "preserved" {
		t.Fatalf("restored xattr=%q err=%v", value[:max(n, 0)], err)
	}
}

func TestRollbackAfterReopenRestoresPreimages(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	existing := filepath.Join(root, "etc", "value")
	created := filepath.Join(root, "usr", "bin", "new")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{existing, created} {
		if err := j.Capture(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(existing, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(created), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(j.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "before" {
		t.Fatalf("restored existing=%q err=%v", data, err)
	}
	if info, err := os.Stat(existing); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode=%v err=%v", info, err)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("created path remains: %v", err)
	}
	if reopened.Status() != "rolled-back" {
		t.Fatalf("status=%s", reopened.Status())
	}
}

func TestCaptureRejectsEscapeAndRootTransaction(t *testing.T) {
	if _, err := Begin(t.TempDir(), string(filepath.Separator)); err == nil {
		t.Fatal("filesystem root accepted")
	}
	root := t.TempDir()
	j, err := Begin(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(filepath.Join(root, "..", "escape")); err == nil {
		t.Fatal("escaping path accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(filepath.Join(root, "linked", "escape")); err == nil {
		t.Fatal("symlink-ancestor escape accepted")
	}
}

func TestCaptureAllowsConfinedRelativeSymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "usr", "lib", "value")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(filepath.Join(root, "lib", "value")); err != nil {
		t.Fatal(err)
	}
	if got := j.state.Entries[0].Path; got != filepath.Join("usr", "lib", "value") {
		t.Fatalf("canonical journal path=%q", got)
	}
}

func TestCaptureRejectsRelativeSymlinkCycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("b", filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(filepath.Join(root, "a", "value")); err == nil {
		t.Fatal("symlink cycle accepted")
	}
}

func TestCaptureRejectsAbsoluteSymlinkInDisposableRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/usr/lib", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(filepath.Join(root, "lib", "value")); err == nil {
		t.Fatal("absolute ancestor symlink accepted without live-root syscall confinement")
	}
}

func TestExplicitLiveRootJournalReopensAndRollsBackConfinedPath(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := BeginLiveRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(j.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "before" {
		t.Fatalf("restored=%q err=%v", data, err)
	}
}

func TestCommitIsDurableAndCannotRollback(t *testing.T) {
	j, err := Begin(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Commit(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(j.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status() != "committed" {
		t.Fatalf("status=%s", reopened.Status())
	}
	if err := reopened.Rollback(); err == nil {
		t.Fatal("committed journal rolled back")
	}
}

func TestRecoverActiveRollsBackInterruptedOperation(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	target := filepath.Join(root, "payload")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Capture(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverActive(base)
	if err != nil || len(recovered) != 1 || recovered[0] != j.Dir() {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "before" {
		t.Fatalf("payload=%q err=%v", data, err)
	}
}

func TestListAndRollbackOneActiveJournal(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "payload")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	operation, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Capture(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	summaries, err := List(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Status != "active" || summaries[0].Entries != 1 {
		t.Fatalf("journal summaries = %#v", summaries)
	}
	rolledBack, err := RollbackActive(base, summaries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Status != "rolled-back" {
		t.Fatalf("rollback summary = %#v", rolledBack)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "old" {
		t.Fatalf("recovered payload = %q, error=%v", data, err)
	}
	if _, err := RollbackActive(base, summaries[0].ID); err == nil {
		t.Fatal("second rollback of inactive journal succeeded")
	}
	if _, err := RollbackActive(base, "../escape"); err == nil {
		t.Fatal("path traversal journal identifier accepted")
	}
}

func TestRemoveTreeRollbackRestoresDirectory(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	tree := filepath.Join(root, "var", "db", "pkg", "cat", "pkg-1")
	if err := os.MkdirAll(filepath.Join(tree, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "CONTENTS"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("CONTENTS", filepath.Join(tree, "nested", "link")); err != nil {
		t.Fatal(err)
	}
	j, err := Begin(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.RemoveTree(tree); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatalf("tree remains: %v", err)
	}
	reopened, err := Open(j.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tree, "CONTENTS"))
	if err != nil || string(data) != "old" {
		t.Fatalf("CONTENTS=%q err=%v", data, err)
	}
	link, err := os.Readlink(filepath.Join(tree, "nested", "link"))
	if err != nil || link != "CONTENTS" {
		t.Fatalf("link=%q err=%v", link, err)
	}
}
