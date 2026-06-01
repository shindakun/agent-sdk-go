//go:build unix

package transport

import (
	"os/exec"
	"syscall"
)

// applyCredential makes the subprocess run as the given OS uid/gid.
func applyCredential(cmd *exec.Cmd, uid, gid int) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid: uint32(uid),
		Gid: uint32(gid),
	}
	return nil
}
