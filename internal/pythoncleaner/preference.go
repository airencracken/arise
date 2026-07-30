package pythoncleaner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func PreferredPolicyTarget(policy Policy) (string, error) {
	if policy.SingleTarget != "" && contains(policy.Targets, policy.SingleTarget) {
		return policy.SingleTarget, nil
	}
	if len(policy.Targets) == 0 {
		return "", fmt.Errorf("python-cleaner: no policy target is available for python-exec")
	}
	return policy.Targets[0], nil
}

func PublishPreference(path, target string) error {
	target = normalizeTarget(target)
	if target == "" {
		return fmt.Errorf("python-cleaner: invalid python-exec target")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("python-cleaner: python-exec preference is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := preferenceContents(data, target)
	if err != nil {
		return err
	}
	return writePreferenceAtomic(path, updated, info.Mode().Perm())
}

func preferenceContents(data []byte, target string) ([]byte, error) {
	var retained []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		value := strings.TrimPrefix(trimmed, "-")
		if normalizeTarget(value) == target {
			continue
		}
		retained = append(retained, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	lines := []string{strings.ReplaceAll(target, "_", ".")}
	lines = append(lines, retained...)
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

type preferenceFile interface {
	io.Writer
	Sync() error
	Chmod(os.FileMode) error
	Close() error
}

var (
	createPreferenceTemp = func(directory, pattern string) (preferenceFile, error) {
		return os.CreateTemp(directory, pattern)
	}
	renamePreference        = os.Rename
	syncPreferenceDirectory = func(path string) error {
		directory, err := os.Open(path)
		if err != nil {
			return err
		}
		err = directory.Sync()
		if closeErr := directory.Close(); err == nil {
			err = closeErr
		}
		return err
	}
)

func writePreferenceAtomic(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	file, err := createPreferenceTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := ""
	if named, ok := file.(interface{ Name() string }); ok {
		temporary = named.Name()
	}
	if temporary == "" {
		_ = file.Close()
		return fmt.Errorf("python-cleaner: temporary preference file has no name")
	}
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := renamePreference(temporary, path); err != nil {
		return err
	}
	return syncPreferenceDirectory(directory)
}
