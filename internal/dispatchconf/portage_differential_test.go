//go:build live_portage

package dispatchconf

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPortageDifferentialFileArchiveAndThreeWayMerge(t *testing.T) {
	python, err := exec.LookPath("python3.14")
	if err != nil {
		t.Skip("Portage Python unavailable")
	}
	probe := exec.Command(python, "-c", "import portage.dispatch_conf")
	if output, err := probe.CombinedOutput(); err != nil {
		t.Skipf("installed Portage Python module unavailable: %v: %s", err, output)
	}

	for _, implementation := range []string{"arise", "portage"} {
		t.Run(implementation, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "etc/value")
			candidatePath := filepath.Join(root, "etc/._cfg0000_value")
			archiveDir := filepath.Join(root, "archive")
			archiveFile := filepath.Join(archiveDir, "etc/value")
			merged := mergedPath(candidatePath)
			writeFixture(t, current, "local=true\nseparator=true\nnew=false\n", 0o600)
			writeFixture(t, candidatePath, "local=false\nseparator=true\nnew=true\n", 0o640)
			writeFixture(t, archiveFile+".dist", "local=false\nseparator=true\nnew=false\n", 0o600)

			if implementation == "arise" {
				opts := DefaultOptions()
				opts.Root = root
				opts.ArchiveDir = archiveDir
				candidate := Candidate{Current: current, New: candidatePath}
				if err := archive(candidate, opts); err != nil {
					t.Fatal(err)
				}
			} else {
				script := `
import portage.dispatch_conf
import sys
portage.dispatch_conf.file_archive(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4])
`
				cmd := exec.CommandContext(context.Background(), python, "-c", script, archiveFile, current, candidatePath, merged)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("Portage file_archive: %v: %s", err, output)
				}
			}

			assertFileContent(t, archiveFile, "local=true\nseparator=true\nnew=false\n")
			assertFileContent(t, archiveFile+".dist.new", "local=false\nseparator=true\nnew=true\n")
			assertFileContent(t, merged, "local=true\nseparator=true\nnew=true\n")
			info, err := os.Stat(merged)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o640 {
				t.Fatalf("merged mode = %o, want 640", info.Mode().Perm())
			}
		})
	}
}

func TestPortageDifferentialSymlinkArchive(t *testing.T) {
	python, err := exec.LookPath("python3.14")
	if err != nil {
		t.Skip("Portage Python unavailable")
	}
	if output, err := exec.Command(python, "-c", "import portage.dispatch_conf").CombinedOutput(); err != nil {
		t.Skipf("installed Portage Python module unavailable: %v: %s", err, output)
	}
	for _, implementation := range []string{"arise", "portage"} {
		t.Run(implementation, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
				t.Fatal(err)
			}
			current := filepath.Join(root, "etc/link")
			candidatePath := filepath.Join(root, "etc/._cfg0000_link")
			if err := os.Symlink("local-target", current); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("new-target", candidatePath); err != nil {
				t.Fatal(err)
			}
			archiveDir := filepath.Join(root, "archive")
			archiveFile := filepath.Join(archiveDir, "etc/link")
			if implementation == "arise" {
				opts := DefaultOptions()
				opts.Root = root
				opts.ArchiveDir = archiveDir
				if err := archive(Candidate{Current: current, New: candidatePath}, opts); err != nil {
					t.Fatal(err)
				}
			} else {
				script := `
import portage.dispatch_conf
import sys
portage.dispatch_conf.file_archive(sys.argv[1], sys.argv[2], sys.argv[3], None)
`
				if output, err := exec.Command(python, "-c", script, archiveFile, current, candidatePath).CombinedOutput(); err != nil {
					t.Fatalf("Portage file_archive: %v: %s", err, output)
				}
			}
			if target, err := os.Readlink(archiveFile); err != nil || target != "local-target" {
				t.Fatalf("archived current symlink = %q, %v", target, err)
			}
			if target, err := os.Readlink(archiveFile + ".dist.new"); err != nil || target != "new-target" {
				t.Fatalf("archived new symlink = %q, %v", target, err)
			}
		})
	}
}
