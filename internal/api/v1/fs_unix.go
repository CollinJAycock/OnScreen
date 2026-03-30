//go:build !windows

package v1

// isRoot returns true when path is the filesystem root on Unix.
func isRoot(path string) bool {
	return path == "/"
}

// rootDirs is not used on Unix; the single root "/" is handled directly.
func rootDirs() []string { return nil }

// normalizeInputPath is a no-op on Unix.
func normalizeInputPath(path string) string { return path }
