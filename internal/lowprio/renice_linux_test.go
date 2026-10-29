package lowprio

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestStartLowersPriority(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := Start(cmd); err != nil {
		t.Skipf("no sleep binary: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	// The raw syscall returns 20-nice, so Nice reads back as 20-Nice.
	prio, err := syscall.Getpriority(syscall.PRIO_PROCESS, cmd.Process.Pid)
	if err != nil {
		t.Fatalf("getpriority: %v", err)
	}
	if got := 20 - prio; got != Nice {
		t.Fatalf("child nice = %d, want %d", got, Nice)
	}
}
