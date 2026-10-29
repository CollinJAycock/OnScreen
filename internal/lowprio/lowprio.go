// Package lowprio runs background media work (trickplay sprites, chapter
// thumbnails, intro and credits detection) so it can't starve playback.
//
// Those jobs run on the server's own CPU, back to back, over every new file
// a scan finds. Left at ffmpeg's defaults each process takes as many threads
// as there are cores and runs at normal priority, so a backlog of new
// episodes held QA's load average at ~12 of 24 threads while competing on
// equal terms with viewers' transcodes and Postgres. Two limits fix that:
//
//   - InputArgs / OutputArgs cap ffmpeg at Threads decoder, filter and
//     encoder threads, so one job uses about Threads cores;
//   - Start / Run / Output / CombinedOutput renice the process to Nice,
//     so whatever it does use is given up the moment anything else wants it.
//
// Playback transcodes deliberately don't use this package.
package lowprio

import (
	"bytes"
	"os/exec"
	"strconv"
)

// Nice is the scheduling niceness of a background job.
const Nice = 10

// Threads caps the decoder, filter and encoder threads of a background
// ffmpeg. The jobs are short and many; throughput comes from the queue
// draining steadily, not from one file finishing faster.
const Threads = 2

// InputArgs caps decoding and filtering. They go before the -i they apply
// to: -threads there is the input's decoder option, and -filter_threads
// (a global option) caps the filter graph, which otherwise takes a thread
// per core regardless of the decoder's limit.
func InputArgs() []string {
	n := strconv.Itoa(Threads)
	return []string{"-filter_threads", n, "-threads", n}
}

// OutputArgs caps the encoder. They go after the last -i, before the
// output path.
func OutputArgs() []string {
	return []string{"-threads", strconv.Itoa(Threads)}
}

// Start starts cmd and lowers its priority to Nice. Lowering after the
// start leaves a window of a few microseconds at normal priority, which is
// harmless; the alternative (wrapping in nice(1)) would depend on a binary
// being on PATH.
func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	renice(cmd.Process.Pid)
	return nil
}

// Run is exec.Cmd.Run through Start.
func Run(cmd *exec.Cmd) error {
	if err := Start(cmd); err != nil {
		return err
	}
	return cmd.Wait()
}

// Output is exec.Cmd.Output through Start: stdout is returned, and stderr
// (unless the caller set cmd.Stderr) is kept on the *exec.ExitError.
func Output(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	captureStderr := cmd.Stderr == nil
	if captureStderr {
		cmd.Stderr = &stderr
	}
	err := Run(cmd)
	if ee, ok := err.(*exec.ExitError); ok && captureStderr {
		ee.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

// CombinedOutput is exec.Cmd.CombinedOutput through Start.
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := Run(cmd)
	return b.Bytes(), err
}
