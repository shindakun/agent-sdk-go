//go:build !unix

package transport

import (
	"errors"
	"os/exec"
)

// applyCredential is unsupported on non-Unix platforms.
func applyCredential(cmd *exec.Cmd, uid, gid int) error {
	return errors.New("running the CLI as a specific user is only supported on Unix")
}
