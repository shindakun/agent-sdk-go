//go:build !unix

package transport

import "io/fs"

// hasExecPermission is satisfied by any regular file on non-Unix platforms.
// Windows file modes do not carry Unix execute bits; executability is governed
// by the file extension (handled by exec.LookPath), so a regular file is
// treated as a candidate here.
func hasExecPermission(mode fs.FileMode) bool {
	return true
}
