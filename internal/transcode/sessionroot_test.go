package transcode

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// useSessionRoot points the session root at dir for one test, removing any
// private fallback prepareSessionRoot creates.
func useSessionRoot(t *testing.T, dir string) {
	t.Helper()
	prev := segmentBaseDir()
	setSegmentBaseDir(dir)
	t.Cleanup(func() {
		if got := segmentBaseDir(); got != dir {
			_ = os.RemoveAll(got)
		}
		setSegmentBaseDir(prev)
	})
}

func TestPrepareSessionRoot_CreatesPrivateRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "onscreen", "sessions")
	useSessionRoot(t, root)
	w := &Worker{logger: nopLogger()}
	w.prepareSessionRoot()
	if got := segmentBaseDir(); got != root {
		t.Fatalf("root moved to %s; a missing root must just be created", got)
	}
	for _, p := range []string{filepath.Dir(root), root} {
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", p, err)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", p, fi.Mode().Perm())
		}
	}
}

// Another local user could plant the fixed <tmp>/onscreen/sessions path as a
// symlink; the startup sweep then RemoveAll'd every directory in its target.
func TestPrepareSessionRoot_SymlinkedRootIsNotSwept(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	keep := filepath.Join(victim, "precious")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "sessions")
	if err := os.Symlink(victim, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	useSessionRoot(t, root)
	w := &Worker{logger: nopLogger()}
	w.prepareSessionRoot()
	w.sweepOrphanedSessions()

	if got := segmentBaseDir(); got == root || !strings.HasPrefix(filepath.Base(got), "onscreen-sessions-") {
		t.Errorf("root = %s, want a private fallback instead of the symlink", got)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("sweep deleted through the symlinked root: %v", err)
	}
}

func TestPrepareSessionRoot_NonDirectoryRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	useSessionRoot(t, root)
	(&Worker{logger: nopLogger()}).prepareSessionRoot()
	got := segmentBaseDir()
	if got == root {
		t.Fatal("a file in the root's place must not be used")
	}
	if fi, err := os.Lstat(got); err != nil || !fi.IsDir() {
		t.Errorf("fallback %s: %v", got, err)
	}
}

func TestEnsurePrivateDir_TightensOwnRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(root); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}
	if fi, _ := os.Lstat(root); fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", fi.Mode().Perm())
	}
}

// A root another user created first (on a shared host, whoever creates the
// fixed temp path owns it) is refused. Needs root to hand a dir to another uid.
func TestEnsurePrivateDir_RefusesForeignOwner(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() != 0 {
		t.Skip("needs root on a Unix host to chown")
	}
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(root, 65534, 65534); err != nil {
		t.Skipf("chown: %v", err)
	}
	if err := ensurePrivateDir(root); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("ensurePrivateDir = %v, want a foreign-owner refusal", err)
	}
}

// Inside a root that is ours, the sweep removes session dirs but never follows
// a link planted among them.
func TestSweepOrphanedSessions_SkipsLinks(t *testing.T) {
	root := withSegmentDir(t)
	victim := filepath.Join(t.TempDir(), "victim")
	keep := filepath.Join(victim, "precious")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "orphan")
	if err := os.Mkdir(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	(&Worker{logger: nopLogger()}).sweepOrphanedSessions()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan session dir not swept: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("sweep followed a link: %v", err)
	}
}

// The ffmpeg command-line log carried the storage-less worker's stream token
// and object storage's presigned credentials.
func TestRedactArgs(t *testing.T) {
	args := []string{
		"-protocol_whitelist", "file,pipe,crypto,data,subfile,http,https,tcp,tls",
		"-i", "http://10.0.0.5:7070/media/stream/abc?token=v4.local.SECRETTOKEN",
		"-i", "https://minio.lan/bucket/movie.mkv?X-Amz-Algorithm=AWS4&X-Amz-Credential=AKIASECRET&X-Amz-Signature=deadbeefSECRET",
		"-i", "/media/Movies/a?b.mkv",
		"-f", "hls", "/tmp/onscreen/sessions/s1/index.m3u8",
	}
	got := redactArgs(args)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("credentials leaked: %s", got)
	}
	for _, want := range []string{
		"http://10.0.0.5:7070/media/stream/abc?REDACTED",
		"https://minio.lan/bucket/movie.mkv?REDACTED",
		"/media/Movies/a?b.mkv", // a local path keeps its literal '?'
		"-f hls /tmp/onscreen/sessions/s1/index.m3u8",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted line lost %q: %s", want, got)
		}
	}
}
