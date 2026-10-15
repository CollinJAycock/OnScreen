package v1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// A remux for a client that lists the file's Dolby Vision profile keeps it
// (fMP4, the record written); profile 7 to an 8.1 client is converted where a
// worker can. Any missing piece leaves the base layer, as before.
func TestStart_DolbyVisionRemux(t *testing.T) {
	const base = "videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:mkv,maxWidth=3840,maxHeight=2160,maxBitDepth=10,hdr=1"
	dvFeatures := []string{transcode.WorkerFeatureDolbyVision}
	convFeatures := []string{transcode.WorkerFeatureDolbyVision, transcode.WorkerFeatureDVConvert}
	cases := []struct {
		name      string
		profile   int
		caps      string
		features  []string
		container string
		want      transcode.DVMode
	}{
		{"8.1 kept for an 8.1 client", 8, base + ",dovi=8.1", dvFeatures, "", transcode.DVKeep},
		{"8.1 to a client without dovi", 8, base, dvFeatures, "", transcode.DVNone},
		{"8.1, old fleet", 8, base + ",dovi=8.1", nil, "", transcode.DVNone},
		{"8.1, client asked for TS", 8, base + ",dovi=8.1", dvFeatures, "ts", transcode.DVNone},
		{"7 converted for an 8.1 client", 7, base + ",dovi=8.1", convFeatures, "", transcode.DVToP81},
		{"7, no converter", 7, base + ",dovi=8.1", dvFeatures, "", transcode.DVNone},
		{"7 kept for a client that decodes 7", 7, base + ",dovi=7:8.1", dvFeatures, "", transcode.DVKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			itemID, user := uuid.New(), uuid.New()
			profile := tc.profile
			file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
				VideoCodec: strPtr("hevc"), AudioCodec: strPtr("aac"), Container: strPtr("matroska"),
				HDRType: strPtr("hdr10"), DVProfile: &profile, VideoBitDepth: ip(10),
				AudioStreams: []byte(`[{"index":1,"codec":"aac","channels":2}]`),
				ResolutionW:  ip(3840), ResolutionH: ip(2160)}
			h, store := newStartHandler(t, file)
			registerWorker(t, store, tc.features)

			rec := startAs(t, h, itemID, user, "192.0.2.10:1", tc.caps,
				transcodeStartRequest{VideoCopy: true, SegmentContainer: tc.container})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			job := drainJob(t, store)
			if job.Encoder != "copy" {
				t.Fatalf("video must be copied, encoder %q", job.Encoder)
			}
			if job.DolbyVision != tc.want {
				t.Errorf("job Dolby Vision %q, want %q", job.DolbyVision, tc.want)
			}
			if tc.want != transcode.DVNone {
				if !job.ForcesFMP4() || !onlySession(t, store).FMP4Segments {
					t.Error("a Dolby Vision copy must be fMP4, job and session alike")
				}
				if job.DVProfileKey == "" {
					t.Error("the job must carry the profile key")
				}
			}
		})
	}
}

// Profile 5 can only be copied: even for a client that lists it, a session
// that would re-encode (asked for, or forced by the client's limits) is
// refused rather than streamed green.
func TestStart_DolbyVisionProfile5ReencodeRefused(t *testing.T) {
	const caps = "videoDecoder=h265,audioDecoder=aac,protocols=mp4:mkv,maxWidth=3840,maxHeight=2160,maxBitDepth=10,dovi=5"
	for _, tc := range []struct {
		name     string
		copy     bool
		caps     string
		wantCode int
	}{
		{"copy plays", true, caps, http.StatusOK},
		{"re-encode asked for", false, caps, http.StatusUnsupportedMediaType},
		{"copy over the client's size", true, strings.Replace(caps, "maxWidth=3840,maxHeight=2160", "maxWidth=1280,maxHeight=720", 1), http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			itemID, user := uuid.New(), uuid.New()
			p := 5
			file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/p5.mkv",
				VideoCodec: strPtr("hevc"), AudioCodec: strPtr("aac"), Container: strPtr("matroska"),
				HDRType: strPtr("dolby_vision"), DVProfile: &p, VideoBitDepth: ip(10),
				AudioStreams: []byte(`[{"index":1,"codec":"aac","channels":2}]`),
				ResolutionW:  ip(3840), ResolutionH: ip(2160)}
			h, store := newStartHandler(t, file)
			registerWorker(t, store, []string{transcode.WorkerFeatureDolbyVision})
			rec := startAs(t, h, itemID, user, "192.0.2.10:1", tc.caps, transcodeStartRequest{VideoCopy: tc.copy})
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				if job := drainJob(t, store); job.DolbyVision != transcode.DVKeep || job.Encoder != "copy" {
					t.Errorf("job %q/%q, want a Dolby Vision copy", job.Encoder, job.DolbyVision)
				}
			} else if !strings.Contains(rec.Body.String(), "DOLBY_VISION_UNSUPPORTED") {
				t.Errorf("want DOLBY_VISION_UNSUPPORTED: %s", rec.Body.String())
			}
		})
	}
}
