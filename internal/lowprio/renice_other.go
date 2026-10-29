//go:build !linux

package lowprio

// renice is a no-op off Linux, where the server runs in production.
func renice(int) {}
