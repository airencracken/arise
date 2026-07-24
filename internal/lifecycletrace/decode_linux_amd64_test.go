//go:build linux && amd64

package lifecycletrace

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
