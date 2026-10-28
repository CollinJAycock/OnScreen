//go:build !linux

package trickplay

// lowerPriority is a no-op off Linux, where the server runs in production.
func lowerPriority(int) {}
