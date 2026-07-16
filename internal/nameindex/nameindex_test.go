package nameindex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	names := []string{"www-client/firefox-bin", "media-fonts/fira", "www-client/firefox", "www-client/firefox"}
	if err := Write(path, names); err != nil {
		t.Fatal(err)
	}
	got, err := Search(path, "firefo", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"www-client/firefox", "www-client/firefox-bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search = %v, want %v", got, want)
	}
	got, err = Search(path, "firefox", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"www-client/firefox"}) {
		t.Fatalf("exact Search = %v", got)
	}
}

func TestSearchRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	if err := Write(path, []string{"app-editors/vim"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("corrupt"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// Appended bytes are deliberately ignored because the versioned payload
	// length allows future sections to be added compatibly.
	if _, err := Search(path, "vim", false); err != nil {
		t.Fatalf("compatible trailing section rejected: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8-1] ^= 0xff
	if err := os.WriteFile(path, data[:len(data)-7], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(path, "vim", false); err == nil {
		t.Fatal("corrupt payload was accepted")
	}
}
