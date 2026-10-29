package lowprio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
)

// TestMain doubles as the child process: with LOWPRIO_HELPER set it writes
// to stdout and stderr and exits with the requested code.
func TestMain(m *testing.M) {
	if os.Getenv("LOWPRIO_HELPER") == "1" {
		fmt.Fprint(os.Stdout, "out")
		fmt.Fprint(os.Stderr, "err")
		if os.Getenv("LOWPRIO_FAIL") == "1" {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(t *testing.T, fail bool) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "LOWPRIO_HELPER=1")
	if fail {
		cmd.Env = append(cmd.Env, "LOWPRIO_FAIL=1")
	}
	return cmd
}

func TestOutput(t *testing.T) {
	out, err := Output(helper(t, false))
	if err != nil || string(out) != "out" {
		t.Fatalf("Output = %q, %v; want \"out\", nil", out, err)
	}
}

func TestOutputKeepsStderrOnExitError(t *testing.T) {
	out, err := Output(helper(t, true))
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *exec.ExitError", err)
	}
	if ee.ExitCode() != 3 || string(ee.Stderr) != "err" || string(out) != "out" {
		t.Fatalf("code %d stderr %q out %q; want 3, \"err\", \"out\"", ee.ExitCode(), ee.Stderr, out)
	}
}

func TestCombinedOutput(t *testing.T) {
	out, err := CombinedOutput(helper(t, false))
	if err != nil || !slices.Contains([]string{"outerr", "errout"}, string(out)) {
		t.Fatalf("CombinedOutput = %q, %v; want both streams", out, err)
	}
}

func TestStartFailure(t *testing.T) {
	if err := Run(exec.Command("onscreen-lowprio-no-such-binary")); err == nil {
		t.Fatal("Run of a missing binary succeeded")
	}
}

func TestThreadArgs(t *testing.T) {
	if got, want := InputArgs(), []string{"-filter_threads", "2", "-threads", "2"}; !slices.Equal(got, want) {
		t.Errorf("InputArgs = %q, want %q", got, want)
	}
	if got, want := OutputArgs(), []string{"-threads", "2"}; !slices.Equal(got, want) {
		t.Errorf("OutputArgs = %q, want %q", got, want)
	}
}
