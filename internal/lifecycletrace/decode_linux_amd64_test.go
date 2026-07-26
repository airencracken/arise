//go:build linux && amd64

package lifecycletrace

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

type fakePaths map[uint64]string

func (f fakePaths) CString(address uint64, _ int) (string, error) {
	value, ok := f[address]
	if !ok {
		return "", fmt.Errorf("missing address %#x", address)
	}
	return value, nil
}

func (f fakePaths) Bytes(address uint64, size int) ([]byte, error) {
	value, ok := f[address]
	if !ok || len(value) < size {
		return nil, fmt.Errorf("missing bytes at %#x", address)
	}
	return []byte(value[:size]), nil
}

type fakeResolver struct{}

func (fakeResolver) Resolve(pid int, dirfd int, path string) (string, error) {
	return fmt.Sprintf("/pid-%d/fd-%d/%s", pid, dirfd, path), nil
}

func (fakeResolver) ResolveFD(pid int, fd int) (string, bool, error) {
	return fmt.Sprintf("/pid-%d/open-fd-%d", pid, fd), true, nil
}

type controlledResolver struct {
	path        string
	journalable bool
	err         error
}

func (r controlledResolver) Resolve(_ int, _ int, _ string) (string, error) {
	return r.path, r.err
}

func (r controlledResolver) ResolveFD(_ int, _ int) (string, bool, error) {
	return r.path, r.journalable, r.err
}

type shortOpenHowReader struct{}

func (shortOpenHowReader) CString(uint64, int) (string, error) {
	return "target", nil
}

func (shortOpenHowReader) Bytes(uint64, int) ([]byte, error) {
	return []byte{unix.O_RDWR}, nil
}

func TestDecodeWriteOpenAndIgnoreReadOpen(t *testing.T) {
	reader := fakePaths{0x1000: "etc/config"}
	dirfd := int64(unix.AT_FDCWD)
	write := Registers{Number: unix.SYS_OPENAT, Args: [6]uint64{uint64(dirfd), 0x1000, unix.O_WRONLY | unix.O_CREAT}}
	paths, relevant, err := Decode(42, write, reader, fakeResolver{})
	if err != nil || !relevant || !reflect.DeepEqual(paths, []string{"/pid-42/fd--100/etc/config"}) {
		t.Fatalf("write open paths=%v relevant=%v err=%v", paths, relevant, err)
	}
	read := write
	read.Args[2] = unix.O_RDONLY
	if paths, relevant, err := Decode(42, read, reader, fakeResolver{}); err != nil || relevant || paths != nil {
		t.Fatalf("read open paths=%v relevant=%v err=%v", paths, relevant, err)
	}
}

func TestDecodeRenameCapturesBothPreimages(t *testing.T) {
	reader := fakePaths{1: "old", 2: "new"}
	registers := Registers{Number: unix.SYS_RENAMEAT2, Args: [6]uint64{7, 1, 8, 2}}
	paths, relevant, err := Decode(9, registers, reader, fakeResolver{})
	want := []string{"/pid-9/fd-7/old", "/pid-9/fd-8/new"}
	if err != nil || !relevant || !reflect.DeepEqual(paths, want) {
		t.Fatalf("rename paths=%v relevant=%v err=%v", paths, relevant, err)
	}
}

func TestDecodeRejectsTmpfile(t *testing.T) {
	reader := fakePaths{1: "directory"}
	dirfd := int64(unix.AT_FDCWD)
	registers := Registers{Number: unix.SYS_OPENAT, Args: [6]uint64{uint64(dirfd), 1, unix.O_TMPFILE | unix.O_RDWR}}
	if _, relevant, err := Decode(1, registers, reader, fakeResolver{}); err == nil || !relevant {
		t.Fatalf("O_TMPFILE relevant=%v err=%v", relevant, err)
	}
}

func TestDecodeOpenat2AndFDMutations(t *testing.T) {
	how := make([]byte, 8)
	binary.LittleEndian.PutUint64(how, unix.O_RDWR|unix.O_CREAT)
	reader := fakePaths{1: "target", 2: string(how)}
	registers := Registers{Number: unix.SYS_OPENAT2, Args: [6]uint64{7, 1, 2, 24}}
	paths, relevant, err := Decode(3, registers, reader, fakeResolver{})
	if err != nil || !relevant || !reflect.DeepEqual(paths, []string{"/pid-3/fd-7/target"}) {
		t.Fatalf("openat2 paths=%v relevant=%v err=%v", paths, relevant, err)
	}
	registers = Registers{Number: unix.SYS_FTRUNCATE, Args: [6]uint64{11}}
	paths, relevant, err = Decode(3, registers, reader, fakeResolver{})
	if err != nil || !relevant || !reflect.DeepEqual(paths, []string{"/pid-3/open-fd-11"}) {
		t.Fatalf("ftruncate paths=%v relevant=%v err=%v", paths, relevant, err)
	}
}

func TestAdversarialDecodeRejectsMalformedReaderAndResolverContracts(t *testing.T) {
	pathSyscall := Registers{Number: unix.SYS_CREAT, Args: [6]uint64{1}}
	for name, value := range map[string]string{"empty": "", "embedded NUL": "etc/\x00passwd"} {
		t.Run(name, func(t *testing.T) {
			paths, relevant, err := Decode(1, pathSyscall, fakePaths{1: value}, fakeResolver{})
			if err == nil || !relevant || paths != nil || !strings.Contains(err.Error(), "invalid empty or NUL path") {
				t.Fatalf("paths=%v relevant=%v err=%v", paths, relevant, err)
			}
		})
	}
	t.Run("path read failure", func(t *testing.T) {
		paths, relevant, err := Decode(1, pathSyscall, fakePaths{}, fakeResolver{})
		if err == nil || !relevant || paths != nil || !strings.Contains(err.Error(), "read syscall path") {
			t.Fatalf("paths=%v relevant=%v err=%v", paths, relevant, err)
		}
	})
	t.Run("relative pathname resolution", func(t *testing.T) {
		paths, relevant, err := Decode(1, pathSyscall, fakePaths{1: "value"}, controlledResolver{path: "relative/value"})
		if err == nil || !relevant || paths != nil || !strings.Contains(err.Error(), "non-absolute") {
			t.Fatalf("paths=%v relevant=%v err=%v", paths, relevant, err)
		}
	})
	t.Run("short openat2 structure", func(t *testing.T) {
		dirfd := int64(unix.AT_FDCWD)
		registers := Registers{Number: unix.SYS_OPENAT2, Args: [6]uint64{uint64(dirfd), 1, 2, 8}}
		paths, relevant, err := Decode(1, registers, shortOpenHowReader{}, fakeResolver{})
		if err == nil || !relevant || paths != nil || !strings.Contains(err.Error(), "short openat2") {
			t.Fatalf("paths=%v relevant=%v err=%v", paths, relevant, err)
		}
	})
}

func TestDecodeFDMutationResolverContract(t *testing.T) {
	registers := Registers{Number: unix.SYS_WRITE, Args: [6]uint64{9}}
	injected := errors.New("resolver failure")
	tests := []struct {
		name     string
		resolver controlledResolver
		relevant bool
		wantErr  string
	}{
		{"non-journalable descriptor", controlledResolver{path: "pipe:[1]", journalable: false}, false, ""},
		{"relative journalable path", controlledResolver{path: "relative", journalable: true}, true, "non-absolute"},
		{"resolver failure", controlledResolver{err: injected}, true, injected.Error()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, relevant, err := Decode(7, registers, fakePaths{}, test.resolver)
			if relevant != test.relevant || paths != nil {
				t.Fatalf("paths=%v relevant=%v", paths, relevant)
			}
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error=%v, want substring %q", err, test.wantErr)
			}
		})
	}

	registers.Args[0] = ^uint64(0)
	if paths, relevant, err := Decode(7, registers, fakePaths{}, fakeResolver{}); err == nil || !relevant || paths != nil || !strings.Contains(err.Error(), "invalid mutation fd") {
		t.Fatalf("negative fd paths=%v relevant=%v err=%v", paths, relevant, err)
	}
}

func TestDecodeSharedWritableMmapRequiresAbsoluteJournalableFD(t *testing.T) {
	registers := Registers{Number: unix.SYS_MMAP, Args: [6]uint64{0, 4096, unix.PROT_WRITE, unix.MAP_SHARED, 12}}
	for _, test := range []struct {
		name     string
		resolver controlledResolver
		relevant bool
		wantErr  bool
	}{
		{"non-journalable", controlledResolver{path: "socket:[1]"}, false, false},
		{"relative path", controlledResolver{path: "relative", journalable: true}, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths, relevant, err := Decode(4, registers, fakePaths{}, test.resolver)
			if paths != nil || relevant != test.relevant || (err != nil) != test.wantErr {
				t.Fatalf("paths=%v relevant=%v err=%v", paths, relevant, err)
			}
		})
	}
}

func TestDecodeSharedWritableMmapAndRejectMountChanges(t *testing.T) {
	registers := Registers{Number: unix.SYS_MMAP, Args: [6]uint64{0, 4096, unix.PROT_READ | unix.PROT_WRITE, unix.MAP_SHARED, 12}}
	paths, relevant, err := Decode(4, registers, fakePaths{}, fakeResolver{})
	if err != nil || !relevant || !reflect.DeepEqual(paths, []string{"/pid-4/open-fd-12"}) {
		t.Fatalf("mmap paths=%v relevant=%v err=%v", paths, relevant, err)
	}
	for _, number := range []uint64{unix.SYS_MOUNT, unix.SYS_CHROOT, unix.SYS_MOVE_MOUNT} {
		if _, relevant, err := Decode(4, Registers{Number: number}, fakePaths{}, fakeResolver{}); err == nil || !relevant {
			t.Fatalf("namespace syscall %d relevant=%v err=%v", number, relevant, err)
		}
	}
}

func TestDecodeFDDataWriteFamilies(t *testing.T) {
	for _, tc := range []struct {
		number uint64
		args   [6]uint64
		fd     int
	}{
		{unix.SYS_WRITE, [6]uint64{13}, 13},
		{unix.SYS_PWRITEV2, [6]uint64{14}, 14},
		{unix.SYS_SENDFILE, [6]uint64{15, 2}, 15},
		{unix.SYS_SPLICE, [6]uint64{2, 0, 16}, 16},
		{unix.SYS_COPY_FILE_RANGE, [6]uint64{2, 0, 17}, 17},
		{unix.SYS_IOCTL, [6]uint64{18}, 18},
	} {
		paths, relevant, err := Decode(5, Registers{Number: tc.number, Args: tc.args}, fakePaths{}, fakeResolver{})
		want := []string{fmt.Sprintf("/pid-5/open-fd-%d", tc.fd)}
		if err != nil || !relevant || !reflect.DeepEqual(paths, want) {
			t.Fatalf("syscall %d paths=%v relevant=%v err=%v", tc.number, paths, relevant, err)
		}
	}
}

func TestProcResolverUsesCwdAndRejectsInvalidDirfd(t *testing.T) {
	got, err := (ProcResolver{}).Resolve(os.Getpid(), unix.AT_FDCWD, "relative/path")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("relative/path")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved=%q want=%q", got, want)
	}
	if _, err := (ProcResolver{}).Resolve(os.Getpid(), -2, "relative"); err == nil {
		t.Fatal("invalid negative dirfd accepted")
	}
}

func TestProcResolverFDClassifiesJournalableTargets(t *testing.T) {
	resolver := ProcResolver{}
	regularPath := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(regularPath, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	regular, err := os.Open(regularPath)
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	resolved, journalable, err := resolver.ResolveFD(os.Getpid(), int(regular.Fd()))
	if err != nil || !journalable || resolved != regularPath {
		t.Fatalf("regular fd resolved=%q journalable=%v err=%v", resolved, journalable, err)
	}

	directoryPath := t.TempDir()
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	resolved, journalable, err = resolver.ResolveFD(os.Getpid(), int(directory.Fd()))
	if err != nil || !journalable || resolved != directoryPath {
		t.Fatalf("directory fd resolved=%q journalable=%v err=%v", resolved, journalable, err)
	}

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readPipe.Close()
	defer writePipe.Close()
	if resolved, journalable, err = resolver.ResolveFD(os.Getpid(), int(readPipe.Fd())); err != nil || journalable || resolved != "" {
		t.Fatalf("pipe fd resolved=%q journalable=%v err=%v", resolved, journalable, err)
	}

	deletedPath := filepath.Join(t.TempDir(), "deleted")
	if err := os.WriteFile(deletedPath, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted, err := os.Open(deletedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer deleted.Close()
	if err := os.Remove(deletedPath); err != nil {
		t.Fatal(err)
	}
	if resolved, journalable, err = resolver.ResolveFD(os.Getpid(), int(deleted.Fd())); err != nil || journalable || resolved != "" {
		t.Fatalf("deleted fd resolved=%q journalable=%v err=%v", resolved, journalable, err)
	}

	if _, _, err := resolver.ResolveFD(os.Getpid(), -1); err == nil {
		t.Fatal("negative fd accepted")
	}
}

func TestAdversarialTraceeReaderRejectsInvalidRequestsBeforePtrace(t *testing.T) {
	reader := traceeReader{pid: os.Getpid()}
	for _, test := range []struct {
		name    string
		address uint64
		size    int
	}{
		{"null address", 0, 1},
		{"zero size", 1, 0},
		{"negative size", 1, -1},
		{"oversized", 1, maxTraceePath + 1},
	} {
		t.Run("bytes "+test.name, func(t *testing.T) {
			if data, err := reader.Bytes(test.address, test.size); err == nil || data != nil || !strings.Contains(err.Error(), "invalid tracee byte request") {
				t.Fatalf("Bytes(%#x, %d)=%v, %v", test.address, test.size, data, err)
			}
		})
	}
	if value, err := reader.CString(0, 1); err == nil || value != "" || !strings.Contains(err.Error(), "null tracee pointer") {
		t.Fatalf("CString null=%q, %v", value, err)
	}
	for _, limit := range []int{0, -1, maxTraceePath + 1} {
		if value, err := reader.CString(1, limit); err == nil || value != "" || !strings.Contains(err.Error(), "invalid tracee string limit") {
			t.Fatalf("CString limit %d=%q, %v", limit, value, err)
		}
	}
}
