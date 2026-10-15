//go:build integration

package downloads

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testdb"
)

// fakeMedia serves the media.File rows the test inserted.
type fakeMedia struct{ files map[uuid.UUID]media.File }

func (f fakeMedia) GetFile(_ context.Context, id uuid.UUID) (*media.File, error) {
	m, ok := f.files[id]
	if !ok {
		return nil, media.ErrNotFound
	}
	return &m, nil
}

func (f fakeMedia) GetFiles(_ context.Context, itemID uuid.UUID) ([]media.File, error) {
	var out []media.File
	for _, m := range f.files {
		if m.MediaItemID == itemID {
			out = append(out, m)
		}
	}
	return out, nil
}

type fixture struct {
	pool   *pgxpool.Pool
	item   uuid.UUID
	file   uuid.UUID
	alice  uuid.UUID
	bob    uuid.UUID
	media  fakeMedia
	source string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	pool := testdb.New(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "movie.mkv")
	gen := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("make source: %v %s", err, out)
	}

	var lib, item, file, alice, bob uuid.UUID
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('m', 'movie', '{/m}') RETURNING id`).Scan(&lib))
	must(pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'Test Movie', 'test movie') RETURNING id`, lib).Scan(&item))
	must(pool.QueryRow(ctx, `INSERT INTO media_files (media_item_id, file_path, file_size, video_codec, duration_ms) VALUES ($1, $2, 1, 'h264', 3000) RETURNING id`, item, src).Scan(&file))
	must(pool.QueryRow(ctx, `INSERT INTO users (username) VALUES ('alice') RETURNING id`).Scan(&alice))
	must(pool.QueryRow(ctx, `INSERT INTO users (username) VALUES ('bob') RETURNING id`).Scan(&bob))

	vc, w, h, dur := "h264", 1280, 720, int64(3000)
	return fixture{
		pool: pool, item: item, file: file, alice: alice, bob: bob, source: src,
		media: fakeMedia{files: map[uuid.UUID]media.File{file: {
			ID: file, MediaItemID: item, FilePath: src, Status: "active",
			VideoCodec: &vc, ResolutionW: &w, ResolutionH: &h, DurationMS: &dur,
		}}},
	}
}

func waitStatus(t *testing.T, svc *Service, jobID uuid.UUID, want string) Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := svc.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == want {
			return j
		}
		if j.Status == StatusFailed && want != StatusFailed {
			t.Fatalf("job failed: %s", j.Error)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", jobID, want)
	return Job{}
}

// TestDownloads_ConvertReuseCleanup_Integration: a conversion runs to a
// playable 720p MP4, a second user's request reuses it without encoding
// again, deleting a job leaves a ready file in place, and the cleanup task
// evicts it once it expires.
func TestDownloads_ConvertReuseCleanup_Integration(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()

	runner := NewRunner(f.pool, f.media, RunnerConfig{Root: root, MaxConcurrent: 1, TTL: time.Hour}, nil)
	svc := NewService(f.pool, root, time.Hour).WithRunner(runner)
	go func() { _ = runner.Run(ctx) }()

	job, created, err := svc.Request(ctx, f.alice, f.item, f.file, "720p_2mbps")
	if err != nil || !created {
		t.Fatalf("request: created=%v err=%v", created, err)
	}
	if !job.Active() {
		t.Fatalf("new job status = %s", job.Status)
	}
	ready := waitStatus(t, svc, job.ID, StatusReady)
	if ready.SizeBytes == nil || *ready.SizeBytes == 0 || ready.ExpiresAt == nil {
		t.Fatalf("ready job missing size/expiry: %+v", ready)
	}
	path, err := svc.ReadyFile(ctx, ready)
	if err != nil {
		t.Fatalf("ready file: %v", err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width,height",
		"-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if s := string(out); !strings.Contains(s, "h264,1280,720") || !strings.Contains(s, "aac") {
		t.Errorf("unexpected output streams: %q", s)
	}

	// Asking again returns the same job.
	again, created, err := svc.Request(ctx, f.alice, f.item, f.file, "720p_2mbps")
	if err != nil || created || again.ID != job.ID {
		t.Fatalf("repeat request: id=%s created=%v err=%v", again.ID, created, err)
	}
	// Bob shares Alice's encode: his job is ready immediately.
	bobJob, created, err := svc.Request(ctx, f.bob, f.item, f.file, "720p_2mbps")
	if err != nil || !created {
		t.Fatalf("bob request: %v", err)
	}
	if bobJob.Status != StatusReady || *bobJob.ConversionID != *ready.ConversionID {
		t.Fatalf("bob should reuse the ready file, got %s conv %v", bobJob.Status, bobJob.ConversionID)
	}

	// Deleting Alice's job leaves the shared file for Bob; not hers to see.
	if err := svc.Delete(ctx, f.bob, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob deleting alice's job: %v", err)
	}
	if err := svc.Delete(ctx, f.alice, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ready file removed with one job: %v", err)
	}

	// Expire it: the cleaner evicts the file and Bob's job reads expired.
	if _, err := f.pool.Exec(ctx, `UPDATE download_conversions SET expires_at = NOW() - INTERVAL '1 minute'`); err != nil {
		t.Fatal(err)
	}
	res, err := NewCleaner(f.pool, root, 1<<30, nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Expired != 1 {
		t.Errorf("expired = %d, want 1 (%s)", res.Expired, res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expired file still on disk: %v", err)
	}
	if j, _ := svc.Get(ctx, bobJob.ID); j.Status != StatusExpired {
		t.Errorf("bob's job = %s, want expired", j.Status)
	}
	if _, err := svc.ReadyFile(ctx, bobJob); err == nil {
		t.Error("expired job still serves a file")
	}

	// Asking again after expiry starts a fresh encode on the same job row.
	redo, created, err := svc.Request(ctx, f.bob, f.item, f.file, "720p_2mbps")
	if err != nil || created || redo.ID != bobJob.ID || !(redo.Active() || redo.Status == StatusReady) {
		t.Fatalf("re-request after expiry: %+v created=%v err=%v", redo, created, err)
	}
	waitStatus(t, svc, redo.ID, StatusReady)
}

// TestDownloads_CancelQueued_Integration: deleting the only job waiting on an
// encode cancels it, and a slot-less runner never starts it.
func TestDownloads_CancelQueued_Integration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	svc := NewService(f.pool, root, time.Hour) // no runner: stays queued

	job, _, err := svc.Request(ctx, f.alice, f.item, f.file, "480p_1mbps")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.ActiveCount(ctx, f.alice); n != 1 {
		t.Fatalf("active count = %d, want 1", n)
	}
	if err := svc.Delete(ctx, f.alice, job.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM download_conversions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("queued conversion with no jobs should be gone, %d left", n)
	}

	// A runner with no free transcode slot claims nothing.
	if _, _, err := svc.Request(ctx, f.alice, f.item, f.file, "480p_1mbps"); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(f.pool, f.media, RunnerConfig{Root: root}, nil).WithSlots(fullSlots{})
	r.fill(ctx)
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM download_conversions`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusQueued {
		t.Errorf("status = %s, want queued while every slot is busy", status)
	}
}

type fullSlots struct{}

func (fullSlots) TryReserveSlot(int) (func(), bool) { return nil, false }

// TestDownloads_StackedMovie_Integration: a two-part movie converts into one
// file as long as both parts.
func TestDownloads_StackedMovie_Integration(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()

	part2 := filepath.Join(filepath.Dir(f.source), "movie-cd2.mkv")
	if err := copyFile(f.source, part2); err != nil {
		t.Fatal(err)
	}
	var file2 uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO media_files (media_item_id, file_path, file_size, video_codec, duration_ms, part_index) VALUES ($1, $2, 1, 'h264', 3000, 2) RETURNING id`, f.item, part2).Scan(&file2); err != nil {
		t.Fatal(err)
	}
	one, two := 1, 2
	p1 := f.media.files[f.file]
	p1.PartIndex = &one
	f.media.files[f.file] = p1
	p2 := p1
	p2.ID, p2.FilePath, p2.PartIndex = file2, part2, &two
	f.media.files[file2] = p2

	runner := NewRunner(f.pool, f.media, RunnerConfig{Root: root, TTL: time.Hour}, nil)
	svc := NewService(f.pool, root, time.Hour).WithRunner(runner)
	go func() { _ = runner.Run(ctx) }()
	job, _, err := svc.Request(ctx, f.alice, f.item, f.file, "480p_1mbps")
	if err != nil {
		t.Fatal(err)
	}
	ready := waitStatus(t, svc, job.ID, StatusReady)
	path, _ := svc.ReadyFile(ctx, ready)
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	if d := strings.TrimSpace(string(out)); !strings.HasPrefix(d, "5.") && !strings.HasPrefix(d, "6.") {
		t.Errorf("joined duration = %s, want ~6 s", d)
	}
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
