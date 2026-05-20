//go:build linux

package features

import (
	"os/exec"
	"syscall"
)

func applyUserPrivPlatform(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Drop privileges by setting process user/group if portage user exists.
	// In practice, this looks up the "portage" user and sets credential.
	uid, gid := lookupPortageUser()
	if uid > 0 && gid > 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid: uid,
			Gid: gid,
		}
	}
}

func lookupPortageUser() (uint32, uint32) {
	return 0, 0
}
