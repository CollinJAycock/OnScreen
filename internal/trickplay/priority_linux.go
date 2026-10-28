package trickplay

import "syscall"

// spriteNice is the scheduling niceness for a sprite run: background work
// that should yield the CPU to playback transcodes and the database.
const spriteNice = 10

// lowerPriority renices a just-started ffmpeg. Best effort: a failure (a
// sandbox without the permission) leaves it at normal priority.
func lowerPriority(pid int) {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, spriteNice)
}
