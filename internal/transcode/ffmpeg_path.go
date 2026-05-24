package transcode

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

// EnsureFFmpegOnPath prepends a bundled ffmpeg directory — an "ffmpeg"
// subdirectory next to the running executable, as the Windows installer lays
// it out — to the process PATH, so the bare "ffmpeg" / "ffprobe" exec calls
// throughout the transcode pipeline resolve to the bundled build.
//
// Without this, a worker.exe (or server.exe) launched outside its Windows
// service — e.g. run directly from a console for debugging — inherits the
// caller's PATH instead of the PATH the service XML injects, and every
// transcode dies with `exec: "ffmpeg": executable file not found in %PATH%`.
// (DetectEncoders fails the same way but silently degrades to software, so
// the only visible symptom is the transcode failure.)
//
// No-op when there's no bundled ffmpeg next to the executable — a source/dev
// build or a Linux install that uses the distro's ffmpeg then relies on the
// system PATH unchanged, so this never shadows an intentionally-chosen ffmpeg.
func EnsureFFmpegOnPath(logger *slog.Logger) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	ffdir := filepath.Join(filepath.Dir(exe), "ffmpeg")
	bin := "ffmpeg"
	if runtime.GOOS == "windows" {
		bin = "ffmpeg.exe"
	}
	if _, err := os.Stat(filepath.Join(ffdir, bin)); err != nil {
		return // no bundled ffmpeg — use the system PATH as-is
	}
	cur := os.Getenv("PATH")
	if cur == "" {
		_ = os.Setenv("PATH", ffdir)
	} else {
		_ = os.Setenv("PATH", ffdir+string(os.PathListSeparator)+cur)
	}
	if logger != nil {
		logger.Info("using bundled ffmpeg", "dir", ffdir)
	}
}
