//go:build !unix

package transcode

import "io/fs"

// ownedByCurrentUser has no uid to compare off Unix (Windows keeps %TEMP% per
// user, so the shared-temp squat it guards against doesn't arise there).
func ownedByCurrentUser(fs.FileInfo) bool { return true }
