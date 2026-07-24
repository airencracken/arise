//go:build linux && amd64

// Package lifecycletrace identifies filesystem preimages that must be made
// durable before a traced lifecycle syscall is allowed to execute.
package lifecycletrace

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const maxTraceePath = 1 << 20

// Registers is the architecture-neutral projection of an amd64 syscall entry.
type Registers struct {
	Number uint64
	Args   [6]uint64
}

type pathReader interface {
	CString(address uint64, limit int) (string, error)
	Bytes(address uint64, size int) ([]byte, error)
}

type pathResolver interface {
	Resolve(pid int, dirfd int, path string) (string, error)
	ResolveFD(pid int, fd int) (path string, journalable bool, err error)
}

// Decode returns the paths whose preimages must be captured before the syscall
// represented by registers executes. relevant is false for a syscall that
// cannot mutate a pathname through this decoder.
func Decode(pid int, registers Registers, reader pathReader, resolver pathResolver) (paths []string, relevant bool, err error) {
	path := func(argument int) (string, error) {
		value, readErr := reader.CString(registers.Args[argument], maxTraceePath)
		if readErr != nil {
			return "", fmt.Errorf("lifecycle trace: read syscall path: %w", readErr)
		}
		if value == "" || strings.IndexByte(value, 0) >= 0 {
			return "", fmt.Errorf("lifecycle trace: invalid empty or NUL path")
		}
		return value, nil
	}
	resolve := func(dirfdArgument, pathArgument int) (string, error) {
		value, readErr := path(pathArgument)
		if readErr != nil {
			return "", readErr
		}
		dirfd := unix.AT_FDCWD
		if dirfdArgument >= 0 {
			dirfd = int(int64(registers.Args[dirfdArgument]))
		}
		resolved, resolveErr := resolver.Resolve(pid, dirfd, value)
		if resolveErr != nil {
			return "", fmt.Errorf("lifecycle trace: resolve %q: %w", value, resolveErr)
		}
		if !filepath.IsAbs(resolved) {
			return "", fmt.Errorf("lifecycle trace: resolver returned non-absolute path %q", resolved)
		}
		return filepath.Clean(resolved), nil
	}
	one := func(dirfdArgument, pathArgument int) ([]string, bool, error) {
		resolved, resolveErr := resolve(dirfdArgument, pathArgument)
		if resolveErr != nil {
			return nil, true, resolveErr
		}
		return []string{resolved}, true, nil
	}
	fdTarget := func(argument int) ([]string, bool, error) {
		fd := int(int64(registers.Args[argument]))
		if fd < 0 {
			return nil, true, fmt.Errorf("lifecycle trace: invalid mutation fd %d", fd)
		}
		resolved, journalable, resolveErr := resolver.ResolveFD(pid, fd)
		if resolveErr != nil {
			return nil, true, fmt.Errorf("lifecycle trace: resolve mutation fd %d: %w", fd, resolveErr)
		}
		if !journalable {
			return nil, false, nil
		}
		return []string{filepath.Clean(resolved)}, true, nil
	}
	two := func(firstDirfd, firstPath, secondDirfd, secondPath int) ([]string, bool, error) {
		first, firstErr := resolve(firstDirfd, firstPath)
		if firstErr != nil {
			return nil, true, firstErr
		}
		second, secondErr := resolve(secondDirfd, secondPath)
		if secondErr != nil {
			return nil, true, secondErr
		}
		return []string{first, second}, true, nil
	}

	switch registers.Number {
	case unix.SYS_OPEN, unix.SYS_OPENAT:
		flagsArgument, dirfdArgument, pathArgument := 1, -1, 0
		if registers.Number == unix.SYS_OPENAT {
			flagsArgument, dirfdArgument, pathArgument = 2, 0, 1
		}
		flags := int(registers.Args[flagsArgument])
		writeFlags := unix.O_WRONLY | unix.O_RDWR | unix.O_CREAT | unix.O_TRUNC | unix.O_APPEND
		if flags&unix.O_TMPFILE == unix.O_TMPFILE {
			return nil, true, fmt.Errorf("lifecycle trace: O_TMPFILE is not yet supported")
		}
		if flags&writeFlags == 0 {
			return nil, false, nil
		}
		return one(dirfdArgument, pathArgument)
	case unix.SYS_OPENAT2:
		if registers.Args[3] < 8 {
			return nil, true, fmt.Errorf("lifecycle trace: invalid openat2 open_how size %d", registers.Args[3])
		}
		how, readErr := reader.Bytes(registers.Args[2], 8)
		if readErr != nil {
			return nil, true, fmt.Errorf("lifecycle trace: read openat2 open_how: %w", readErr)
		}
		flags := binary.LittleEndian.Uint64(how)
		writeFlags := uint64(unix.O_WRONLY | unix.O_RDWR | unix.O_CREAT | unix.O_TRUNC | unix.O_APPEND)
		if flags&uint64(unix.O_TMPFILE) == uint64(unix.O_TMPFILE) {
			return nil, true, fmt.Errorf("lifecycle trace: openat2 O_TMPFILE is not yet supported")
		}
		if flags&writeFlags == 0 {
			return nil, false, nil
		}
		return one(0, 1)
	case unix.SYS_CREAT, unix.SYS_TRUNCATE, unix.SYS_UNLINK, unix.SYS_RMDIR,
		unix.SYS_MKDIR, unix.SYS_MKNOD, unix.SYS_CHMOD, unix.SYS_CHOWN,
		unix.SYS_LCHOWN, unix.SYS_UTIME, unix.SYS_UTIMES, unix.SYS_SETXATTR,
		unix.SYS_LSETXATTR, unix.SYS_REMOVEXATTR, unix.SYS_LREMOVEXATTR:
		return one(-1, 0)
	case unix.SYS_UNLINKAT, unix.SYS_MKDIRAT, unix.SYS_MKNODAT, unix.SYS_FCHOWNAT,
		unix.SYS_FCHMODAT, unix.SYS_FCHMODAT2, unix.SYS_FUTIMESAT, unix.SYS_UTIMENSAT, unix.SYS_SYMLINKAT:
		pathArgument := 1
		if registers.Number == unix.SYS_SYMLINKAT {
			pathArgument = 2
		}
		return one(0, pathArgument)
	case unix.SYS_RENAME, unix.SYS_LINK:
		return two(-1, 0, -1, 1)
	case unix.SYS_RENAMEAT, unix.SYS_RENAMEAT2, unix.SYS_LINKAT:
		return two(0, 1, 2, 3)
	case unix.SYS_SYMLINK:
		return one(-1, 1)
	case unix.SYS_FTRUNCATE, unix.SYS_FCHMOD, unix.SYS_FCHOWN,
		unix.SYS_FSETXATTR, unix.SYS_FREMOVEXATTR:
		return fdTarget(0)
	case unix.SYS_WRITE, unix.SYS_WRITEV, unix.SYS_PWRITE64, unix.SYS_PWRITEV,
		unix.SYS_PWRITEV2, unix.SYS_FALLOCATE, unix.SYS_VMSPLICE, unix.SYS_IOCTL:
		return fdTarget(0)
	case unix.SYS_SENDFILE:
		return fdTarget(0) // out_fd
	case unix.SYS_SPLICE:
		return fdTarget(2) // fd_out
	case unix.SYS_COPY_FILE_RANGE:
		return fdTarget(2) // fd_out
	case unix.SYS_MMAP:
		prot, flags := registers.Args[2], registers.Args[3]
		if prot&unix.PROT_WRITE == 0 || flags&unix.MAP_SHARED == 0 {
			return nil, false, nil
		}
		fd := int(int64(registers.Args[4]))
		if fd < 0 {
			return nil, false, nil
		}
		resolved, journalable, resolveErr := resolver.ResolveFD(pid, fd)
		if resolveErr != nil {
			return nil, true, fmt.Errorf("lifecycle trace: resolve shared writable mmap fd %d: %w", fd, resolveErr)
		}
		if !journalable {
			return nil, false, nil
		}
		return []string{filepath.Clean(resolved)}, true, nil
	case unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT,
		unix.SYS_OPEN_TREE, unix.SYS_MOVE_MOUNT, unix.SYS_FSOPEN, unix.SYS_FSMOUNT,
		unix.SYS_FSPICK, unix.SYS_MOUNT_SETATTR, unix.SYS_OPEN_TREE_ATTR:
		return nil, true, fmt.Errorf("lifecycle trace: mount/root-changing syscall %d is unsupported", registers.Number)
	case unix.SYS_UNSHARE:
		if registers.Args[0]&unix.CLONE_NEWNS != 0 {
			return nil, true, fmt.Errorf("lifecycle trace: mount namespace unshare is unsupported")
		}
		return nil, false, nil
	case unix.SYS_SETNS:
		// A zero namespace type permits joining any namespace type, so only an
		// explicit non-mount type can be ignored safely.
		if registers.Args[1] == 0 || registers.Args[1]&unix.CLONE_NEWNS != 0 {
			return nil, true, fmt.Errorf("lifecycle trace: mount-capable setns is unsupported")
		}
		return nil, false, nil
	case unix.SYS_SETXATTRAT, unix.SYS_REMOVEXATTRAT:
		return nil, true, fmt.Errorf("lifecycle trace: xattr-at syscall %d is not yet decoded", registers.Number)
	case unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER:
		return nil, true, fmt.Errorf("lifecycle trace: io_uring is unsupported during transactional lifecycle phases")
	default:
		return nil, false, nil
	}
}
