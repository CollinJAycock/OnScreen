//go:build unix

package transcode

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether fi belongs to this process's user — the
// session root must not be a directory another local user created first.
func ownedByCurrentUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
