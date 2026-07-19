package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

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
