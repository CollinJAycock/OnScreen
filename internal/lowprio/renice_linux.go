package lowprio

import "syscall"

// renice lowers a just-started process to Nice. Best effort: a failure (a
// sandbox without the permission) leaves it at normal priority.
func renice(pid int) {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, Nice)
}
