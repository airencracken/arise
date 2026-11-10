package binpkg

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureInstalledConfigUpdateLineage(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, format := range []struct {
			name   string
			create func(context.Context, string, string, string) (string, error)
		}{
			{"recovery", Create},
			{"gpkg", CreateInstalledGPKG},
		} {
			name := format.name + "/consumed"
			if pending {
				name = format.name + "/pending"
			}
			t.Run(name, func(t *testing.T) {
				base := t.TempDir()
				const contents = "dir /etc/pam.d\nobj /etc/pam.d/._cfg0000_runuser digest 1700000000\nobj /etc/pam.d/._cfg0001_runuser digest 1700000000\n"
				vdb, root := createCaptureFixture(t, base, contents)
				directory := filepath.Join(root, "etc/pam.d")
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "runuser"), []byte("current PAM configuration"), 0640); err != nil {
					t.Fatal(err)
				}
				if pending {
					if err := os.WriteFile(filepath.Join(directory, "._cfg0001_runuser"), []byte("pending PAM configuration"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				artifact, err := format.create(context.Background(), vdb, root, filepath.Join(base, "packages"))
				if err != nil {
					t.Fatal(err)
				}
				if format.name == "recovery" {
					manifest, err := ReadRecoveryManifest(artifact)
					if err != nil {
						t.Fatal(err)
					}
					wantEntries := 4
					if len(manifest.Payload) != wantEntries {
						t.Fatalf("payload contains %d entries, want %d", len(manifest.Payload), wantEntries)
					}
					absent := 0
					for _, item := range manifest.Payload {
						if item.Type == "absent" {
							absent++
						}
					}
					wantAbsent := 2
					if pending {
						wantAbsent = 1
					}
					if absent != wantAbsent {
						t.Fatalf("absent config records = %d; want %d", absent, wantAbsent)
					}
					foundContents := false
					for _, evidence := range manifest.SourceVDB {
						if evidence.Path == "CONTENTS" {
							foundContents = true
							decoded, err := base64.StdEncoding.DecodeString(evidence.ContentBase64)
							if err != nil || string(decoded) != contents {
								t.Fatalf("source VDB evidence changed: %q, %v", decoded, err)
							}
						}
					}
					if !foundContents {
						t.Fatal("source VDB CONTENTS evidence is missing")
					}
				}
				destination := t.TempDir()
				if err := Extract(context.Background(), artifact, destination); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(destination, "etc/pam.d/runuser"))
				if err != nil || string(data) != "current PAM configuration" {
					t.Fatalf("current config was not restored: %q, %v", data, err)
				}
				if _, err := os.Lstat(filepath.Join(destination, "etc/pam.d/._cfg0000_runuser")); !os.IsNotExist(err) {
					t.Fatalf("consumed update was recreated: %v", err)
				}
				if pending {
					data, err := os.ReadFile(filepath.Join(destination, "etc/pam.d/._cfg0001_runuser"))
					if err != nil || string(data) != "pending PAM configuration" {
						t.Fatalf("pending update was not restored: %q, %v", data, err)
					}
				}
			})
		}
	}
}

func TestCaptureMissingOrdinaryAndConfigLookalikesWithoutInferringOtherFiles(t *testing.T) {
	for _, name := range []string{"runuser", "._cfg000_runuser", "._cfg00000_runuser", "._cfgxxxx_runuser", "._cfg0000_", "._cfg0000_..", "._cfg0000_."} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			vdb, root := createCaptureFixture(t, base, "obj /etc/pam.d/"+name+" digest 1700000000\n")
			if err := os.MkdirAll(filepath.Join(root, "etc/pam.d"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "etc/pam.d/runuser"), []byte("current"), 0644); err != nil {
				t.Fatal(err)
			}
			if name == "runuser" {
				if err := os.Remove(filepath.Join(root, "etc/pam.d/runuser")); err != nil {
					t.Fatal(err)
				}
			}
			artifact, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := ReadRecoveryManifest(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.Payload) != 1 || manifest.Payload[0].Type != "absent" || manifest.Payload[0].Path != "etc/pam.d/"+name {
				t.Fatalf("capture inferred configuration lineage for an ordinary missing path: %+v", manifest.Payload)
			}
		})
	}
}

func TestCaptureConfigUpdateRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /etc/pam.d/._cfg0000_runuser digest 1700000000\n")
	if err := os.MkdirAll(filepath.Join(root, "etc/pam.d"), 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "etc/pam.d/runuser")); err != nil {
		t.Fatal(err)
	}
	if artifact, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages")); err == nil || artifact != "" || !strings.Contains(err.Error(), "changed type") {
		t.Fatalf("capture accepted replacement symlink: %q, %v", artifact, err)
	}
}

func TestCaptureConfigUpdateBoundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
		pending  bool
		current  bool
		wantErr  bool
	}{
		{"neither file exists", "obj /etc/._cfg0000_runuser digest 1\n", false, false, false},
		{"pending without current", "obj /etc/._cfg0000_runuser digest 1\n", true, false, false},
		{"current explicitly owned", "obj /etc/._cfg0000_runuser digest 1\nobj /etc/runuser digest 1\n", false, true, false},
		{"duplicate original record", "obj /etc/._cfg0000_runuser digest 1\nobj /etc/._cfg0000_runuser digest 1\n", true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			vdb, root := createCaptureFixture(t, base, test.contents)
			if err := os.MkdirAll(filepath.Join(root, "etc"), 0755); err != nil {
				t.Fatal(err)
			}
			for name, exists := range map[string]bool{"._cfg0000_runuser": test.pending, "runuser": test.current} {
				if exists {
					if err := os.WriteFile(filepath.Join(root, "etc", name), []byte(name), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			artifact, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
			if test.wantErr {
				if err == nil || artifact != "" {
					t.Fatalf("unsafe capture succeeded: %q, %v", artifact, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadRecoveryManifest(artifact); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCaptureConfigUpdateRejectsSymlinkParents(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /etc/pam.d/._cfg0000_runuser digest 1\n")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "runuser"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "etc/pam.d")); err != nil {
		t.Fatal(err)
	}
	if artifact, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages")); err == nil || artifact != "" || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("capture followed a parent symlink: %q, %v", artifact, err)
	}
}
