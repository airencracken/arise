package perlcleaner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/journal"
)

type Leftover struct {
	Path  string `json:"path"`
	Known bool   `json:"known"`
}

var knownLeftoverSuffixes = []string{
	"/XML/SAX/ParserDetails.ini",
	"/_h2ph_pre.ph",
	"/asm-generic/bitsperlong.ph",
	"/asm-generic/ioctl.ph",
	"/asm-generic/ioctls.ph",
	"/asm-generic/posix_types.ph",
	"/asm-generic/socket.ph",
	"/asm-generic/sockios.ph",
	"/asm-generic/termbits.ph",
	"/asm-generic/termios.ph",
	"/asm-generic/unistd.ph",
	"/asm/bitsperlong.ph",
	"/asm/ioctl.ph",
	"/asm/ioctls.ph",
	"/asm/posix_types.ph",
	"/asm/posix_types_32.ph",
	"/asm/posix_types_64.ph",
	"/asm/posix_types_x32.ph",
	"/asm/socket.ph",
	"/asm/sockios.ph",
	"/asm/termbits.ph",
	"/asm/termios.ph",
	"/asm/unistd.ph",
	"/asm/unistd_32.ph",
	"/asm/unistd_64.ph",
	"/asm/unistd_x32.ph",
	"/bits/byteswap-16.ph",
	"/bits/byteswap.ph",
	"/bits/endian.ph",
	"/bits/ioctl-types.ph",
	"/bits/ioctls.ph",
	"/bits/pthreadtypes.ph",
	"/bits/select.ph",
	"/bits/select2.ph",
	"/bits/sigaction.ph",
	"/bits/sigcontext.ph",
	"/bits/siginfo.ph",
	"/bits/signum.ph",
	"/bits/sigset.ph",
	"/bits/sigstack.ph",
	"/bits/sigthread.ph",
	"/bits/sockaddr.ph",
	"/bits/socket.ph",
	"/bits/socket2.ph",
	"/bits/socket_type.ph",
	"/bits/syscall.ph",
	"/bits/syslog-ldbl.ph",
	"/bits/syslog-path.ph",
	"/bits/syslog.ph",
	"/bits/time.ph",
	"/bits/timex.ph",
	"/bits/types.ph",
	"/bits/typesizes.ph",
	"/bits/uio.ph",
	"/bits/waitflags.ph",
	"/bits/waitstatus.ph",
	"/bits/wordsize.ph",
	"/endian.ph",
	"/features.ph",
	"/gnu/stubs-32.ph",
	"/gnu/stubs-64.ph",
	"/gnu/stubs.ph",
	"/ioctl.ph",
	"/posix_types.ph",
	"/signal.ph",
	"/stdarg.ph",
	"/stdc-predef.ph",
	"/stddef.ph",
	"/sys/cdefs.ph",
	"/sys/ioctl.ph",
	"/sys/select.ph",
	"/sys/socket.ph",
	"/sys/syscall.ph",
	"/sys/syslog.ph",
	"/sys/sysmacros.ph",
	"/sys/time.ph",
	"/sys/ttydefaults.ph",
	"/sys/types.ph",
	"/sys/ucontext.ph",
	"/sys/uio.ph",
	"/sys/wait.ph",
	"/syscall.ph",
	"/sysexits.ph",
	"/syslimits.ph",
	"/syslog.ph",
	"/time.ph",
	"/wait.ph",
	"/xlocale.ph",
	"/PDL/Index.pod",
	"/PDL/pdldoc.db",
}

func FindLeftovers(root string, abi ABI) ([]Leftover, error) {
	var result []Leftover
	for _, relative := range []string{
		"usr/share/perl5", "usr/lib/perl5", "usr/lib32/perl5", "usr/lib64/perl5", "usr/libx32/perl5",
	} {
		base := filepath.Join(root, filepath.FromSlash(relative))
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return filepath.SkipDir
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			relativeToRoot, err := filepath.Rel(root, path)
			if err != nil || strings.HasPrefix(relativeToRoot, "..") {
				return fmt.Errorf("perl-cleaner: leftover path escaped root: %s", path)
			}
			display := "/" + filepath.ToSlash(relativeToRoot)
			if !staleModulePath(display, abi) {
				return nil
			}
			result = append(result, Leftover{Path: path, Known: knownLeftover(display)})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	if result == nil {
		return []Leftover{}, nil
	}
	return result, nil
}

func knownLeftover(path string) bool {
	for _, suffix := range knownLeftoverSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

var removeLeftover = os.Remove

func DeleteKnown(root, journalDir string, leftovers []Leftover) (returnErr error) {
	var paths []string
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, leftover := range leftovers {
		if !leftover.Known {
			continue
		}
		path, err := filepath.Abs(leftover.Path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(cleanRoot, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("perl-cleaner: refusing leftover outside root: %s", leftover.Path)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil
	}
	var operation *journal.Journal
	if cleanRoot == string(filepath.Separator) {
		operation, err = journal.BeginLiveRoot(journalDir)
	} else {
		operation, err = journal.Begin(journalDir, cleanRoot)
	}
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			returnErr = errors.Join(returnErr, operation.Rollback())
		}
	}()
	if err := operation.CaptureBatch(paths); err != nil {
		return err
	}
	for _, path := range paths {
		if err := removeLeftover(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := operation.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
