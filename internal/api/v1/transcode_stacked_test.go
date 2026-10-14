package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// stackedFiles is a two-part rip ("cd1" + "cd2") of one movie, part 2 listed
// first the way quality order can put it, plus a separate 4K version.
func stackedFiles(itemID uuid.UUID) (cd1, cd2, uhd media.File) {
	mk := func(path string, part int, dur int64) media.File {
		f := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: path, Status: "active",
			VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
			ResolutionW: ip(1920), ResolutionH: ip(1080), DurationMS: &dur}
		if part > 0 {
			f.PartIndex = ip(part)
		}
		return f
	}
	return mk("/media/Movie (2001)/Movie (2001) cd1.mp4", 1, 3_000_000),
		mk("/media/Movie (2001)/Movie (2001) cd2.mp4", 2, 2_400_000),
		mk("/media/Movie (2001)/Movie (2001) - 4K.mp4", 0, 5_400_000)
}

func newStackedHandler(t *testing.T, files ...media.File) (*NativeTranscodeHandler, *transcode.SessionStore) {
	t.Helper()
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	h := NewNativeTranscodeHandler(store, transcode.NewSegmentTokenManager(v), &mockTranscodeMedia{
		item:  &media.Item{ID: files[0].MediaItemID, Type: "movie", Title: "Movie"},
		files: files,
	}, &config.Config{TranscodeMaxHeight: 2160, TranscodeMaxWidth: 3840, TranscodeMaxBitrate: 40000}, slog.Default()).
		WithVerifySource(fakeSourceOK)
	return h, store
}

// Start with ANY part's file_id streams the whole movie from part 1, and the
// job and session carry every part in order with the combined length.
func TestStart_StackedMovieStreamsAllParts(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	cd1, cd2, uhd := stackedFiles(itemID)
	h, store := newStackedHandler(t, cd2, uhd, cd1)

	fid := cd2.ID.String()
	rec := startAs(t, h, itemID, user, "192.0.2.10:1", "", transcodeStartRequest{FileID: &fid, VideoCopy: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	job := drainJob(t, store)
	if len(job.Parts) != 2 || job.Parts[0].FileID != cd1.ID.String() || job.Parts[1].FileID != cd2.ID.String() {
		t.Fatalf("job parts = %+v, want cd1, cd2", job.Parts)
	}
	if job.FilePath != cd1.FilePath {
		t.Errorf("job FilePath = %q, want part 1 (an old worker plays that)", job.FilePath)
	}
	if transcode.PartsDurationSec(job.Parts) != 5400 {
		t.Errorf("combined length = %v, want 5400", transcode.PartsDurationSec(job.Parts))
	}
	s := onlySession(t, store)
	if s.FileID != cd1.ID || len(s.Parts) != 2 {
		t.Errorf("session file=%s parts=%d, want cd1 with 2 parts", s.FileID, len(s.Parts))
	}
}

// A file outside any stack, and a lone "cd1", play exactly as before.
func TestStart_UnstackedFilesCarryNoParts(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	cd1, _, uhd := stackedFiles(itemID)
	h, store := newStackedHandler(t, uhd, cd1)
	for _, f := range []media.File{uhd, cd1} {
		fid := f.ID.String()
		rec := startAs(t, h, itemID, user, "192.0.2.10:1", "", transcodeStartRequest{FileID: &fid, VideoCopy: true})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		job := drainJob(t, store)
		if job.Parts != nil || job.FilePath != f.FilePath {
			t.Errorf("%s: job parts=%v path=%q, want none / its own path", f.FilePath, job.Parts, job.FilePath)
		}
	}
}

// A damaged second part refuses the movie up-front, like a damaged file.
func TestStart_StackedMovieWithDamagedPartRefused(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	cd1, cd2, _ := stackedFiles(itemID)
	cd2.IntegrityStatus = media.IntegrityDamaged
	h, store := newStackedHandler(t, cd1, cd2)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1", "", transcodeStartRequest{VideoCopy: true})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if code, _ := errorCode(t, rec); code != "FILE_DAMAGED" {
		t.Errorf("code = %q", code)
	}
	if all, _ := store.List(context.Background()); len(all) != 0 {
		t.Errorf("a refused Start created %d sessions", len(all))
	}
}

// The playback decision never says directPlay for a part: one file is one
// part of the movie.
func TestDecision_StackedMovieIsNeverDirectPlay(t *testing.T) {
	itemID := uuid.New()
	cd1, cd2, uhd := stackedFiles(itemID)
	h, _ := newStackedHandler(t, cd1, cd2, uhd)
	decide := func(fileID uuid.UUID) string {
		body, _ := json.Marshal(map[string]string{"file_id": fileID.String()})
		req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		req = withClaims(withChiParam(req, "id", itemID.String()))
		req.Header.Set("X-Client-Capabilities", "videoDecoder=h264,audioDecoder=aac,protocols=mp4")
		rec := httptest.NewRecorder()
		h.Decision(rec, req)
		var out struct {
			Data playbackDecisionResponse `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Data.Decision + " " + out.Data.FileID
	}
	if got, want := decide(cd2.ID), "directStream "+cd1.ID.String(); got != want {
		t.Errorf("part 2: got %q, want %q", got, want)
	}
	if got, want := decide(uhd.ID), "directPlay "+uhd.ID.String(); got != want {
		t.Errorf("unstacked version: got %q, want %q", got, want)
	}
}
