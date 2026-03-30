package v1

import (
	"os"
	"strings"
)

// isRoot returns true when path is the virtual "list drives" sentinel on Windows.
func isRoot(path string) bool {
	return path == "/" || path == `\`
}

// rootDirs returns the available drive root directories on Windows.
func rootDirs() []string {
	var roots []string
	for _, drive := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		root := string(drive) + `:\`
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// normalizeInputPath converts a path received from the UI to an OS-appropriate path.
// On Windows, forward slashes are converted to backslashes.
func normalizeInputPath(path string) string {
	return strings.ReplaceAll(path, "/", `\`)
}
