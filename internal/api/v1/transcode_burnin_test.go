package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// registerWorker puts one worker in the fleet. Addr "" makes its queue the one
// drainJob reads; features nil is a worker built before any of them.
func registerWorker(t *testing.T, store *transcode.SessionStore, features []string) {
	t.Helper()
	if err := store.RegisterWorker(context.Background(), transcode.WorkerRegistration{
		ID: "w-" + uuid.NewString(), Capabilities: []string{"libx264"}, MaxSessions: 8,
		Features: features, RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
}

var allWorkerFeatures = []string{transcode.WorkerFeatureBurnSubtitles, transcode.WorkerFeatureAudioPassthrough}

// seedPlaying is the session the viewer is watching when they ask for a new
// one: a refused request must leave it running.
func seedPlaying(t *testing.T, store *transcode.SessionStore, user uuid.UUID, file media.File) transcode.Session {
	t.Helper()
	prior := transcode.Session{ID: transcode.NewSessionID(), UserID: user, MediaItemID: file.MediaItemID,
		FileID: file.ID, Decision: "transcode", CreatedAt: time.Now().UTC()}
	if err := store.Create(context.Background(), prior); err != nil {
		t.Fatalf("seed prior session: %v", err)
	}
	return prior
}

// ── Audio passthrough on the remux path (capability profiles Phase 3) ───────

// A 7.1 TrueHD Blu-ray .m2ts on an Android TV that bitstreams TrueHD: the
// remux copies the track (above the client's 5.1 cap — the receiver decodes
// it) into fMP4 segments, since no player demuxes TrueHD out of MPEG-TS. Each
// precondition missing — the audioPassthrough key, a worker that knows how,
// a client that will take fMP4 — leaves the old AAC conversion.
func TestStart_AudioPassthroughRemux(t *testing.T) {
	const base = "videoDecoder=h264,audioDecoder=aac:ac3:eac3:truehd,protocols=mp4:mkv:ts,maxAudioChannels=6"
	const passthrough = base + ",audioPassthrough=ac3:eac3:truehd:dts"
	cases := []struct {
		name      string
		audio     string
		caps      string
		features  []string
		container string // segment_container
		wantCopy  bool
	}{
		{"truehd passthrough copies into fMP4", "truehd", passthrough, allWorkerFeatures, "", true},
		{"dts passthrough copies into fMP4", "dts", passthrough, allWorkerFeatures, "", true},
		{"no audioPassthrough: converted as before", "truehd", base, allWorkerFeatures, "", false},
		{"old fleet: converted", "truehd", passthrough, nil, "", false},
		{"client asked for TS: converted", "truehd", passthrough, allWorkerFeatures, "ts", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			itemID, user := uuid.New(), uuid.New()
			file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.m2ts",
				VideoCodec: strPtr("h264"), AudioCodec: strPtr(tc.audio), Container: strPtr("mpegts"),
				AudioStreams: []byte(`[{"index":1,"codec":"` + tc.audio + `","channels":8}]`),
				ResolutionW:  ip(1920), ResolutionH: ip(1080)}
			h, store := newStartHandler(t, file)
			registerWorker(t, store, tc.features)

			rec := startAs(t, h, itemID, user, "192.0.2.10:1", tc.caps,
				transcodeStartRequest{VideoCopy: true, SegmentContainer: tc.container})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			job := drainJob(t, store)
			if job.Encoder != "copy" {
				t.Fatalf("video must still be copied, encoder %q", job.Encoder)
			}
			s := onlySession(t, store)
			if tc.wantCopy {
				if job.AudioCodec != "copy" || !job.AudioPassthrough || !job.ForcesFMP4() {
					t.Errorf("job audio=%q passthrough=%v fmp4=%v, want a copy into fMP4",
						job.AudioCodec, job.AudioPassthrough, job.ForcesFMP4())
				}
				if !s.FMP4Segments || s.VideoOutput().SegExt() != ".m4s" {
					t.Errorf("session must be waited on as .m4s (FMP4Segments=%v)", s.FMP4Segments)
				}
				if s.OutputAudioCodec != "copy" || s.OutputAudioChannels != 8 {
					t.Errorf("Now Playing audio %q/%d, want copy/8", s.OutputAudioCodec, s.OutputAudioChannels)
				}
				return
			}
			if job.AudioCodec != "aac" || job.AudioPassthrough || job.ForcesFMP4() {
				t.Errorf("job audio=%q passthrough=%v fmp4=%v, want the AAC conversion",
					job.AudioCodec, job.AudioPassthrough, job.ForcesFMP4())
			}
			if s.FMP4Segments {
				t.Error("an AAC remux must not promise fMP4")
			}
		})
	}
}

// A passed-through AC3/E-AC3 above the client's channel cap is copied too, and
// it needs nothing new from the worker: both ride MPEG-TS as they always have.
func TestStart_AudioPassthroughAC3AboveChannelCap(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("eac3"), Container: strPtr("matroska"),
		AudioStreams: []byte(`[{"index":1,"codec":"eac3","channels":8}]`),
		ResolutionW:  ip(1920), ResolutionH: ip(1080)}
	h, store := newStartHandler(t, file)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"videoDecoder=h264,audioDecoder=aac:ac3:eac3,protocols=mp4,maxAudioChannels=2,audioPassthrough=eac3",
		transcodeStartRequest{VideoCopy: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	job := drainJob(t, store)
	if job.AudioCodec != "copy" || job.AudioPassthrough {
		t.Errorf("job audio=%q passthrough=%v, want a plain copy", job.AudioCodec, job.AudioPassthrough)
	}
}

// ── Subtitle burn-in ────────────────────────────────────────────────────────

// fakeOffsets serves subtitle_stream_offsets and the OCR rows of a file.
type fakeOffsets struct {
	stream int32
	ext    []gen.ExternalSubtitle
}

func (f *fakeOffsets) GetSubtitleStreamOffset(context.Context, gen.GetSubtitleStreamOffsetParams) (int32, error) {
	return f.stream, nil
}

func (f *fakeOffsets) ListSubtitleStreamOffsets(context.Context, []uuid.UUID) ([]gen.ListSubtitleStreamOffsetsRow, error) {
	return nil, nil
}

func (f *fakeOffsets) List(context.Context, uuid.UUID) ([]gen.ExternalSubtitle, error) {
	return f.ext, nil
}

// burnFile has a text track at absolute index 2 and a PGS track at 3.
func burnFile(itemID uuid.UUID) media.File {
	return media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("matroska"),
		AudioStreams:    []byte(`[{"index":1,"codec":"aac","channels":2}]`),
		SubtitleStreams: []byte(`[{"index":2,"codec":"subrip"},{"index":3,"codec":"hdmv_pgs_subtitle"}]`),
		ResolutionW:     ip(1920), ResolutionH: ip(1080), DurationMS: ptrInt64(600_000)}
}

func ptrInt64(v int64) *int64 { return &v }

// Every refusal answers with its code BEFORE Start supersedes anything: the
// stream the viewer is watching keeps playing, and nothing is dispatched.
func TestStart_BurnInRefusals(t *testing.T) {
	cases := []struct {
		name     string
		idx      int
		stacked  bool
		features []string
		status   int
		code     string
	}{
		{"not a subtitle stream of the file", 9, false, allWorkerFeatures, http.StatusBadRequest, "INVALID_SUBTITLE_STREAM"},
		{"the audio stream's index", 1, false, allWorkerFeatures, http.StatusBadRequest, "INVALID_SUBTITLE_STREAM"},
		{"a text subtitle", 2, false, allWorkerFeatures, http.StatusBadRequest, "SUBTITLE_NOT_IMAGE"},
		{"a stacked movie", 3, true, allWorkerFeatures, http.StatusBadRequest, "BURN_IN_UNSUPPORTED"},
		{"no worker can burn", 3, false, []string{transcode.WorkerFeatureAudioPassthrough}, http.StatusServiceUnavailable, "BURN_IN_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			itemID, user := uuid.New(), uuid.New()
			file := burnFile(itemID)
			var h *NativeTranscodeHandler
			var store *transcode.SessionStore
			if tc.stacked {
				file.FilePath, file.PartIndex = "/media/Movie/Movie cd1.mkv", ip(1)
				cd2 := file
				cd2.ID, cd2.FilePath, cd2.PartIndex = uuid.New(), "/media/Movie/Movie cd2.mkv", ip(2)
				h, store = newStackedHandler(t, file, cd2)
			} else {
				h, store = newStartHandler(t, file)
			}
			registerWorker(t, store, tc.features)
			prior := seedPlaying(t, store, user, file)

			rec := startAs(t, h, itemID, user, "192.0.2.10:1", "",
				transcodeStartRequest{BurnSubtitleStreamIndex: ip(tc.idx)})
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if code, _ := errorCode(t, rec); code != tc.code {
				t.Errorf("code %q, want %q", code, tc.code)
			}
			if _, err := store.Get(ctx, prior.ID); err != nil {
				t.Errorf("a refused burn-in stopped the playing session: %v", err)
			}
			if job, _ := store.DequeueJob(ctx, "", 100*time.Millisecond); job != nil {
				t.Errorf("a refused burn-in dispatched a job: %+v", job)
			}
		})
	}
}

// A burn-in is a single-rendition re-encode whatever was asked: video_copy is
// overridden, the ABR ladder (on, Auto quality) is skipped, the worker job
// carries the absolute stream index and its offset, and the response echoes it.
func TestStart_BurnInStartsSingleReencode(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := burnFile(itemID)
	h, store := newStartHandler(t, file)
	h.cfg.TranscodeABR = true
	ocrID := "stream_3"
	offsets := &fakeOffsets{ext: []gen.ExternalSubtitle{
		{ID: uuid.New(), Source: "opensubtitles", OffsetMs: 900},
		{ID: uuid.New(), Source: "ocr", SourceID: &ocrID, OffsetMs: -350},
	}}
	h.WithBurnInOffsets(offsets, offsets)
	registerWorker(t, store, allWorkerFeatures)
	prior := seedPlaying(t, store, user, file)

	rec := startAs(t, h, itemID, user, "192.0.2.10:1", "videoDecoder=h264,audioDecoder=aac",
		transcodeStartRequest{VideoCopy: true, BurnSubtitleStreamIndex: ip(3)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data transcodeStartResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.BurnedSubtitleStreamIndex == nil || *resp.Data.BurnedSubtitleStreamIndex != 3 {
		t.Errorf("burned_subtitle_stream_index = %v, want 3", resp.Data.BurnedSubtitleStreamIndex)
	}

	job := drainJob(t, store)
	if job.Encoder == "copy" || job.Decision != "transcode" {
		t.Errorf("a burn-in must re-encode: encoder %q decision %q", job.Encoder, job.Decision)
	}
	if job.BurnSubtitle == nil || *job.BurnSubtitle != (transcode.SubtitleBurn{StreamIndex: 3, OffsetMS: -350}) {
		t.Errorf("job burn = %+v, want stream 3 shifted by its OCR version's -350 ms", job.BurnSubtitle)
	}
	s, err := store.Get(context.Background(), job.SessionID)
	if err != nil || s.ABR {
		t.Fatalf("want a single-rendition session, got %+v (%v)", s, err)
	}
	if _, err := store.Get(context.Background(), prior.ID); err == nil {
		t.Error("a started burn-in must supersede the session it replaces")
	}

	// The stream's own admin offset wins over its OCR version's.
	offsets.stream = 1200
	if got := h.burnInOffsetMS(context.Background(), file.ID, 3); got != 1200 {
		t.Errorf("offset = %d, want the stream's 1200", got)
	}
}

// Without a burn-in the response doesn't mention one, and the job has none.
func TestStart_NoBurnInByDefault(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := burnFile(itemID)
	h, store := newStartHandler(t, file)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1", "", transcodeStartRequest{Height: 720})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var raw map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if _, ok := raw["data"]["burned_subtitle_stream_index"]; ok {
		t.Errorf("response mentions a burn-in: %s", rec.Body.String())
	}
	if job := drainJob(t, store); job.BurnSubtitle != nil {
		t.Errorf("job carries a burn-in: %+v", job.BurnSubtitle)
	}
}
