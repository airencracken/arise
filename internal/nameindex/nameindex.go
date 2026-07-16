// Package nameindex implements the compact, immutable package-name index.
package nameindex

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Filename = "names.arise"
	version  = uint32(1)
)

var magic = [8]byte{'A', 'R', 'I', 'S', 'E', 'N', 'I', '\n'}

type header struct {
	Magic    [8]byte
	Version  uint32
	Count    uint32
	Size     uint64
	Checksum [sha256.Size]byte
}

// Path returns the name-index path associated with a Badger database directory.
func Path(dbPath string) string { return filepath.Join(dbPath, Filename) }

// Write atomically writes a sorted package-name index.
func Write(path string, names []string) error {
	names = append([]string(nil), names...)
	sort.Strings(names)
	names = compact(names)
	if uint64(len(names)) > uint64(^uint32(0)) {
		return errors.New("name index: too many package records")
	}
	var payload bytes.Buffer
	for _, name := range names {
		if name == "" || strings.ContainsRune(name, '\n') {
			return fmt.Errorf("name index: invalid package name %q", name)
		}
		payload.WriteString(name)
		payload.WriteByte('\n')
	}
	h := header{Magic: magic, Version: version, Count: uint32(len(names)), Size: uint64(payload.Len()), Checksum: sha256.Sum256(payload.Bytes())}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("name index: create directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".names-*.tmp")
	if err != nil {
		return fmt.Errorf("name index: create temporary file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	w := bufio.NewWriterSize(f, 256<<10)
	if err := binary.Write(w, binary.LittleEndian, &h); err == nil {
		_, err = w.Write(payload.Bytes())
	}
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("name index: write: %w", err)
	}
	if err := os.Chmod(tmp, 0644); err != nil {
		return fmt.Errorf("name index: chmod: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("name index: replace: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("name index: open directory for sync: %w", err)
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return fmt.Errorf("name index: sync directory: %w", err)
	}
	ok = true
	return nil
}

// Search returns canonical category/package names matching query.
func Search(path, query string, exact bool) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var h header
	if err := binary.Read(f, binary.LittleEndian, &h); err != nil {
		return nil, fmt.Errorf("name index: read header: %w", err)
	}
	if h.Magic != magic || h.Version != version {
		return nil, errors.New("name index: unsupported or corrupt format")
	}
	if h.Size > 1<<30 {
		return nil, errors.New("name index: invalid payload size")
	}
	payload := make([]byte, int(h.Size))
	if _, err := io.ReadFull(f, payload); err != nil {
		return nil, fmt.Errorf("name index: read payload: %w", err)
	}
	if sha256.Sum256(payload) != h.Checksum {
		return nil, errors.New("name index: checksum mismatch")
	}
	needle := strings.ToLower(query)
	var matches []string
	for _, raw := range bytes.Split(payload, []byte{'\n'}) {
		if len(raw) == 0 {
			continue
		}
		cp := string(raw)
		parts := strings.SplitN(cp, "/", 2)
		if len(parts) != 2 {
			return nil, errors.New("name index: invalid package record")
		}
		categoryLower, packageLower := strings.ToLower(parts[0]), strings.ToLower(parts[1])
		matched := strings.Contains(categoryLower, needle) || strings.Contains(packageLower, needle)
		if exact {
			matched = categoryLower == needle || packageLower == needle
		}
		if matched {
			matches = append(matches, cp)
		}
	}
	if uint32(len(bytes.Fields(payload))) != h.Count {
		return nil, errors.New("name index: record count mismatch")
	}
	return matches, nil
}

func compact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
