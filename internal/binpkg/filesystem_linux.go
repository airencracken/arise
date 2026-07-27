//go:build linux

package binpkg

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

const (
	xattrPAXPrefix = "ARISE.xattr."
	sparsePAXKey   = "ARISE.sparse.extents"
)

type SparseExtent struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

func readExtendedAttributes(path string, symlink bool) (map[string]string, error) {
	size, err := listXattr(path, nil, symlink)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	names := make([]byte, size)
	size, err = listXattr(path, names, symlink)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, name := range splitNullNames(names[:size]) {
		valueSize, err := getXattr(path, name, nil, symlink)
		if err != nil {
			return nil, fmt.Errorf("read xattr %s: %w", name, err)
		}
		value := make([]byte, valueSize)
		if valueSize > 0 {
			if _, err := getXattr(path, name, value, symlink); err != nil {
				return nil, fmt.Errorf("read xattr %s: %w", name, err)
			}
		}
		result[name] = base64.StdEncoding.EncodeToString(value)
	}
	return result, nil
}

func splitNullNames(data []byte) []string {
	var names []string
	start := 0
	for index, value := range data {
		if value == 0 {
			if index > start {
				names = append(names, string(data[start:index]))
			}
			start = index + 1
		}
	}
	sort.Strings(names)
	return names
}

func listXattr(path string, dest []byte, symlink bool) (int, error) {
	if symlink {
		return unix.Llistxattr(path, dest)
	}
	return unix.Listxattr(path, dest)
}

func getXattr(path, name string, dest []byte, symlink bool) (int, error) {
	if symlink {
		return unix.Lgetxattr(path, name, dest)
	}
	return unix.Getxattr(path, name, dest)
}

func applyExtendedAttributes(path string, records map[string]string, symlink bool) error {
	for key, encoded := range records {
		if len(key) <= len(xattrPAXPrefix) || key[:len(xattrPAXPrefix)] != xattrPAXPrefix {
			continue
		}
		name := key[len(xattrPAXPrefix):]
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("decode xattr %s: %w", name, err)
		}
		if symlink {
			err = unix.Lsetxattr(path, name, value, 0)
		} else {
			err = unix.Setxattr(path, name, value, 0)
		}
		if err != nil {
			return fmt.Errorf("restore xattr %s: %w", name, err)
		}
	}
	return nil
}

func sparseMap(path string, size int64) ([]SparseExtent, error) {
	if size == 0 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var extents []SparseExtent
	for offset := int64(0); offset < size; {
		data, err := unix.Seek(int(file.Fd()), offset, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			break
		}
		if errors.Is(err, unix.EINVAL) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		hole, err := unix.Seek(int(file.Fd()), data, unix.SEEK_HOLE)
		if err != nil {
			return nil, err
		}
		if hole > size {
			hole = size
		}
		extents = append(extents, SparseExtent{Offset: data, Length: hole - data})
		offset = hole
	}
	if len(extents) == 1 && extents[0].Offset == 0 && extents[0].Length == size {
		return nil, nil
	}
	return extents, nil
}

func encodeSparseMap(extents []SparseExtent) (string, error) {
	data, err := json.Marshal(extents)
	return string(data), err
}

func setSymlinkTimes(path string, atime, mtime time.Time) error {
	times := []unix.Timespec{unix.NsecToTimespec(atime.UnixNano()), unix.NsecToTimespec(mtime.UnixNano())}
	return unix.UtimesNanoAt(unix.AT_FDCWD, path, times, unix.AT_SYMLINK_NOFOLLOW)
}
