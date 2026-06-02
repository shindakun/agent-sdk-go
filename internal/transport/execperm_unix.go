//go:build unix

package transport

import "io/fs"

// hasExecPermission reports whether a regular file's mode grants execute
// permission to any of user/group/other.
func hasExecPermission(mode fs.FileMode) bool {
	return mode&0o111 != 0
}
