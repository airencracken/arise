//go:build linux && amd64

package lifecycletrace

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

type traceeReader struct{ pid int }

func (r traceeReader) Bytes(address uint64, size int) ([]byte, error) {
	if address == 0 || size <= 0 || size > maxTraceePath {
		return nil, fmt.Errorf("invalid tracee byte request address=%#x size=%d", address, size)
	}
	buffer := make([]byte, size)
	n, err := unix.PtracePeekData(r.pid, uintptr(address), buffer)
	if err != nil {
		return nil, err
	}
	if n != size {
		return nil, fmt.Errorf("short tracee read: got %d want %d", n, size)
	}
	return buffer, nil
}

func (r traceeReader) CString(address uint64, limit int) (string, error) {
	if address == 0 {
		return "", fmt.Errorf("null tracee pointer")
	}
	if limit <= 0 || limit > maxTraceePath {
		return "", fmt.Errorf("invalid tracee string limit %d", limit)
	}
	buffer := make([]byte, 0, 256)
	chunk := make([]byte, 256)
	for len(buffer) < limit {
		want := len(chunk)
		if remaining := limit - len(buffer); remaining < want {
			want = remaining
		}
		n, err := unix.PtracePeekData(r.pid, uintptr(address)+uintptr(len(buffer)), chunk[:want])
		if err != nil && n == 0 {
			return "", err
		}
		if n == 0 {
			return "", fmt.Errorf("ptrace returned no path data")
		}
		if end := bytes.IndexByte(chunk[:n], 0); end >= 0 {
			return string(append(buffer, chunk[:end]...)), nil
		}
		buffer = append(buffer, chunk[:n]...)
	}
	return "", fmt.Errorf("tracee path exceeds %d bytes", limit)
}

// RunPrototype starts command under ptrace and calls beforeMutation while each
// tracee is stopped at a recognized filesystem-mutating syscall entry. It is
// intentionally not wired to package execution until the complete fail-closed
// syscall matrix and cancellation behavior are certified.
func RunPrototype(command *exec.Cmd, beforeMutation func([]string) error) error {
	if command == nil || beforeMutation == nil {
		return fmt.Errorf("lifecycle trace: command and mutation callback are required")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	if command.SysProcAttr.Credential != nil {
		return fmt.Errorf("lifecycle trace: prototype does not yet support credential launch")
	}
	command.SysProcAttr.Ptrace = true
	if err := command.Start(); err != nil {
		return err
	}
	rootPID := command.Process.Pid
	var status unix.WaitStatus
	if _, err := unix.Wait4(rootPID, &status, 0, nil); err != nil {
		return fmt.Errorf("lifecycle trace: initial wait: %w", err)
	}
	if !status.Stopped() {
		return fmt.Errorf("lifecycle trace: child did not enter initial ptrace stop")
	}
	options := unix.PTRACE_O_TRACESYSGOOD | unix.PTRACE_O_TRACEFORK | unix.PTRACE_O_TRACEVFORK | unix.PTRACE_O_TRACECLONE | unix.PTRACE_O_EXITKILL
	if err := unix.PtraceSetOptions(rootPID, options); err != nil {
		return fmt.Errorf("lifecycle trace: set options: %w", err)
	}
	live := map[int]bool{rootPID: true}
	defer func() {
		// On every early return, terminate and reap all tracees. EXITKILL protects
		// tracer-process death; it does not clean up when this function reports a
		// capture or decoding error while Arise itself remains alive.
		for pid := range live {
			_ = unix.Kill(pid, unix.SIGKILL)
		}
		for pid := range live {
			var cleanupStatus unix.WaitStatus
			_, _ = unix.Wait4(pid, &cleanupStatus, unix.WALL, nil)
		}
	}()
	entering := make(map[int]bool)
	if err := unix.PtraceSyscall(rootPID, 0); err != nil {
		return fmt.Errorf("lifecycle trace: start syscall observation: %w", err)
	}
	rootExit := 0
	for len(live) != 0 {
		pid, err := unix.Wait4(-1, &status, unix.WALL, nil)
		if err != nil {
			return fmt.Errorf("lifecycle trace: wait: %w", err)
		}
		if status.Exited() {
			if pid == rootPID {
				rootExit = status.ExitStatus()
			}
			delete(live, pid)
			delete(entering, pid)
			continue
		}
		if status.Signaled() {
			delete(live, pid)
			delete(entering, pid)
			if pid == rootPID {
				return fmt.Errorf("lifecycle trace: command terminated by %s", status.Signal())
			}
			continue
		}
		if !status.Stopped() {
			continue
		}
		event := int(status) >> 16
		if event == unix.PTRACE_EVENT_FORK || event == unix.PTRACE_EVENT_VFORK || event == unix.PTRACE_EVENT_CLONE {
			child, eventErr := unix.PtraceGetEventMsg(pid)
			if eventErr != nil {
				return fmt.Errorf("lifecycle trace: read child event: %w", eventErr)
			}
			live[int(child)] = true
			if err := unix.PtraceSyscall(pid, 0); err != nil {
				return fmt.Errorf("lifecycle trace: resume parent: %w", err)
			}
			continue
		}
		stop := status.StopSignal()
		if stop == unix.SIGTRAP|0x80 {
			if !entering[pid] {
				var regs unix.PtraceRegs
				if err := unix.PtraceGetRegs(pid, &regs); err != nil {
					return fmt.Errorf("lifecycle trace: read registers: %w", err)
				}
				projected := Registers{Number: regs.Orig_rax, Args: [6]uint64{regs.Rdi, regs.Rsi, regs.Rdx, regs.R10, regs.R8, regs.R9}}
				paths, relevant, decodeErr := Decode(pid, projected, traceeReader{pid: pid}, ProcResolver{})
				if decodeErr != nil {
					return decodeErr
				}
				if relevant {
					if err := beforeMutation(paths); err != nil {
						return fmt.Errorf("lifecycle trace: capture preimage: %w", err)
					}
				}
			}
			entering[pid] = !entering[pid]
			if err := unix.PtraceSyscall(pid, 0); err != nil {
				return fmt.Errorf("lifecycle trace: resume syscall: %w", err)
			}
			continue
		}
		signal := int(stop)
		if stop == unix.SIGSTOP || stop == unix.SIGTRAP {
			signal = 0
		}
		if err := unix.PtraceSyscall(pid, signal); err != nil {
			return fmt.Errorf("lifecycle trace: resume signal stop: %w", err)
		}
	}
	if rootExit != 0 {
		return fmt.Errorf("lifecycle trace: command exited with status %d", rootExit)
	}
	return nil
}
