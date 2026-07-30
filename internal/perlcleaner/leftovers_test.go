package perlcleaner

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFindLeftoversClassifiesOnlyStaleRegularFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"usr/lib64/perl5/vendor_perl/5.40/XML/SAX/ParserDetails.ini": "known",
		"usr/lib64/perl5/vendor_perl/5.40/Custom.pm":                 "unknown",
		"usr/lib64/perl5/vendor_perl/5.42/Current.pm":                "current",
		"usr/lib64/perl5/5.42/sgmlspl-specs/skel.pl":                 "current-data",
	}
	for relative, data := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.pm")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "usr/lib64/perl5/vendor_perl/5.40/link.pm")); err != nil {
		t.Fatal(err)
	}
	got, err := FindLeftovers(root, ABI{Version: "5.42", Arch: "x86_64-linux"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Leftover{
		{Path: filepath.Join(root, "usr/lib64/perl5/vendor_perl/5.40/Custom.pm"), Known: false},
		{Path: filepath.Join(root, "usr/lib64/perl5/vendor_perl/5.40/XML/SAX/ParserDetails.ini"), Known: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("leftovers = %#v, want %#v", got, want)
	}
}

func TestDeleteKnownPreservesUnknownAndMetadata(t *testing.T) {
	root := t.TempDir()
	known := filepath.Join(root, "usr/lib64/perl5/5.40/features.ph")
	unknown := filepath.Join(root, "usr/lib64/perl5/5.40/Custom.pm")
	for path, data := range map[string]string{known: "known", unknown: "unknown"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	leftovers := []Leftover{{Path: known, Known: true}, {Path: unknown}}
	if err := DeleteKnown(root, filepath.Join(root, "journals"), leftovers); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(known); !os.IsNotExist(err) {
		t.Fatalf("known leftover remains: %v", err)
	}
	data, err := os.ReadFile(unknown)
	if err != nil || string(data) != "unknown" {
		t.Fatalf("unknown leftover changed: %q %v", data, err)
	}
	if err := DeleteKnown(root, filepath.Join(root, "journals-2"), leftovers); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestDeleteKnownRejectsTraversalWithoutMutation(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.ph")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteKnown(root, filepath.Join(root, "journals"), []Leftover{{Path: outside, Known: true}}); err == nil {
		t.Fatal("outside deletion accepted")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("outside changed: %q %v", data, err)
	}
}

func TestDeleteKnownRollsBackOnPartialFailure(t *testing.T) {
	root := t.TempDir()
	var leftovers []Leftover
	for _, name := range []string{"one.ph", "two.ph"} {
		path := filepath.Join(root, "usr/lib64/perl5/5.40", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
		leftovers = append(leftovers, Leftover{Path: path, Known: true})
	}
	oldRemove := removeLeftover
	t.Cleanup(func() { removeLeftover = oldRemove })
	calls := 0
	removeLeftover = func(path string) error {
		calls++
		if calls == 2 {
			return errors.New("injected delete failure")
		}
		return os.Remove(path)
	}
	err := DeleteKnown(root, filepath.Join(root, "journals"), leftovers)
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("DeleteKnown error = %v", err)
	}
	for _, leftover := range leftovers {
		data, readErr := os.ReadFile(leftover.Path)
		if readErr != nil || string(data) != filepath.Base(leftover.Path) {
			t.Fatalf("rollback %s = %q %v", leftover.Path, data, readErr)
		}
		info, statErr := os.Stat(leftover.Path)
		if statErr != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("rollback metadata %s = %v %v", leftover.Path, info, statErr)
		}
	}
}
