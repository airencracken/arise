//go:build !linux

package features

import "os/exec"

func applyUserPrivPlatform(cmd *exec.Cmd) {}
