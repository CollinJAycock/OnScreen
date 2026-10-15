package v1

import (
	"net/http"
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
