//go:build linux

package merge

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
)

func copyXattrs(source, target string, noFollow bool) error {
	list := unix.Listxattr
	get := unix.Getxattr
	set := unix.Setxattr
	if noFollow {
		list = unix.Llistxattr
		get = unix.Lgetxattr
		set = unix.Lsetxattr
	}
	size, err := list(source, nil)
	if err != nil {
		return fmt.Errorf("list xattrs on %s: %w", source, err)
	}
	if size == 0 {
		return nil
	}
	names := make([]byte, size)
	if _, err := list(source, names); err != nil {
		return fmt.Errorf("read xattr names on %s: %w", source, err)
	}
	for _, rawName := range bytes.Split(names, []byte{0}) {
		if len(rawName) == 0 {
			continue
		}
		name := string(rawName)
		valueSize, err := get(source, name, nil)
		if err != nil {
			return fmt.Errorf("size xattr %s on %s: %w", name, source, err)
		}
		value := make([]byte, valueSize)
		if _, err := get(source, name, value); err != nil {
			return fmt.Errorf("read xattr %s on %s: %w", name, source, err)
		}
		if err := set(target, name, value, 0); err != nil {
			return fmt.Errorf("write xattr %s on %s: %w", name, target, err)
		}
	}
	return nil
}
