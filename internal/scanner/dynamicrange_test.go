package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// An unchanged file an older scan tagged "dolby_vision" is re-read once, on
// the fast skip, and only its dynamic range is written.
func TestProcessFile_UnchangedDolbyVisionFileIsReRead(t *testing.T) {
	sp := func(s string) *string { return &s }
	tests := []struct {
		name        string
		hdr         *string // stored hdr_type
		profile     *int    // stored dv_profile
		probe       *ProbeResult
		probeErr    error
		saveErr     error
		probes      int // over two scans
		wantSaves   int
		wantHDR     *string // stored hdr_type after the scans
		wantProfile *int
	}{
		{name: "profile 8 record over SDR video becomes SDR", hdr: sp("dolby_vision"),
			probe:  &ProbeResult{VideoCodec: sp("hevc"), DVProfile: ip(8)},
			probes: 1, wantSaves: 1, wantHDR: nil, wantProfile: ip(8)},
		{name: "profile 7 becomes HDR10", hdr: sp("dolby_vision"),
			probe:  &ProbeResult{VideoCodec: sp("hevc"), HDRType: sp("hdr10"), DVProfile: ip(7)},
			probes: 1, wantSaves: 1, wantHDR: sp("hdr10"), wantProfile: ip(7)},
		{name: "profile 5 stays Dolby Vision, profile recorded", hdr: sp("dolby_vision"),
			probe:  &ProbeResult{VideoCodec: sp("hevc"), HDRType: sp("dolby_vision"), DVProfile: ip(5)},
			probes: 1, wantSaves: 1, wantHDR: sp("dolby_vision"), wantProfile: ip(5)},
		{name: "already re-read: profile recorded", hdr: sp("dolby_vision"), profile: ip(5),
			probes: 0, wantSaves: 0, wantHDR: sp("dolby_vision"), wantProfile: ip(5)},
		{name: "HDR10 file is left alone", hdr: sp("hdr10"),
			probes: 0, wantSaves: 0, wantHDR: sp("hdr10")},
		{name: "SDR file is left alone",
			probes: 0, wantSaves: 0},
		{name: "probe fails: tried once per process, row unchanged", hdr: sp("dolby_vision"),
			probeErr: errors.New("ffprobe: exit status 1"),
			probes:   1, wantSaves: 0, wantHDR: sp("dolby_vision")},
		{name: "probe finds no video: row unchanged", hdr: sp("dolby_vision"),
			probe:  &ProbeResult{},
			probes: 1, wantSaves: 0, wantHDR: sp("dolby_vision")},
		{name: "save fails: row unchanged, not retried this process", hdr: sp("dolby_vision"),
			probe:   &ProbeResult{VideoCodec: sp("hevc"), HDRType: sp("hdr10"), DVProfile: ip(7)},
			saveErr: errors.New("connection reset"),
			probes:  1, wantSaves: 1, wantHDR: sp("dolby_vision")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Film (2001)", "Film (2001).mkv")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("not really a film"), 0o644); err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			svc := newMockMediaService()
			svc.dynamicRangeErr = tc.saveErr
			poster := "poster.jpg" // enriched: nothing else surfaces the item
			movie := &media.Item{ID: uuid.New(), Type: "movie", Title: "Film", PosterPath: &poster}
			svc.items[movie.ID] = movie
			hash := "unchanged"
			file := &media.File{ID: uuid.New(), MediaItemID: movie.ID, FilePath: path, FileSize: info.Size(),
				FileHash: &hash, Status: "active", ScannedAt: time.Now(), HDRType: tc.hdr, DVProfile: tc.profile}
			svc.fileByPath[path] = file

			s := newTestScanner(svc)
			probes := 0
			s.probeFile = func(_ context.Context, p string) (*ProbeResult, error) {
				probes++
				if p != path {
					t.Errorf("probed %q, want %q", p, path)
				}
				return tc.probe, tc.probeErr
			}
			for scan := 1; scan <= 2; scan++ {
				if _, _, isNew, err := s.processFile(context.Background(), uuid.New(), "movie", path, []string{root}); err != nil || isNew {
					t.Fatalf("scan %d: processFile = isNew %v, err %v", scan, isNew, err)
				}
			}
			if len(svc.fileCalls) != 0 || len(svc.hierarchyCalls) != 0 {
				t.Errorf("file left the fast skip: %d upserts, %d hierarchy calls", len(svc.fileCalls), len(svc.hierarchyCalls))
			}
			if probes != tc.probes {
				t.Errorf("probed %d times over two scans, want %d", probes, tc.probes)
			}
			if len(svc.dynamicRangeCalls) != tc.wantSaves {
				t.Fatalf("SetFileDynamicRange calls = %d, want %d", len(svc.dynamicRangeCalls), tc.wantSaves)
			}
			if tc.wantSaves > 0 && tc.saveErr == nil {
				c := svc.dynamicRangeCalls[0]
				if c.id != file.ID || hdrOf(c.hdrType) != hdrOf(tc.wantHDR) || !samePos(c.dvProfile, tc.wantProfile) {
					t.Errorf("saved (%v, %q, %v), want (%v, %q, %v)", c.id, hdrOf(c.hdrType), deref(c.dvProfile),
						file.ID, hdrOf(tc.wantHDR), deref(tc.wantProfile))
				}
			}
			if hdrOf(file.HDRType) != hdrOf(tc.wantHDR) || !samePos(file.DVProfile, tc.wantProfile) {
				t.Errorf("row now (%q, %v), want (%q, %v)", hdrOf(file.HDRType), deref(file.DVProfile),
					hdrOf(tc.wantHDR), deref(tc.wantProfile))
			}
		})
	}
}

// hdrOf prints a stored hdr_type, "" for SDR (nil).
func hdrOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
