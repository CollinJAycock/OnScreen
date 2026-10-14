package downloads

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

func TestQualitiesFollowTheLadder(t *testing.T) {
	want := map[string][3]int{ // bitrate, width, height
		"1080p_8mbps": {8000, 1920, 1080},
		"720p_4mbps":  {4000, 1280, 720},
		"720p_2mbps":  {2000, 1280, 720},
		"480p_1mbps":  {1000, 854, 480},
	}
	if len(Qualities) != len(want) {
		t.Fatalf("got %d qualities, want %d", len(Qualities), len(want))
	}
	for _, q := range Qualities {
		w, ok := want[q.ID]
		if !ok {
			t.Fatalf("unexpected quality %s", q.ID)
		}
		if q.Profile.Bitrate != w[0] || q.Profile.MaxWidth != w[1] || q.Profile.MaxHeight != w[2] {
			t.Errorf("%s = %+v, want %v", q.ID, q.Profile, w)
		}
	}
	if q, _ := QualityByID("720p_4mbps"); q.Label != "720p · 4 Mbps" {
		t.Errorf("label = %q", q.Label)
	}
	if _, ok := QualityByID("original"); ok {
		t.Error("original is not a conversion")
	}
}

func TestAllowedUnderCap(t *testing.T) {
	allowed := func(capKbps int) []string {
		var out []string
		for _, q := range Qualities {
			if q.AllowedUnder(capKbps) {
				out = append(out, q.ID)
			}
		}
		return out
	}
	cases := map[int]string{
		0:     "1080p_8mbps,720p_4mbps,720p_2mbps,480p_1mbps",
		20000: "1080p_8mbps,720p_4mbps,720p_2mbps,480p_1mbps",
		4000:  "720p_4mbps,720p_2mbps,480p_1mbps", // capped at 4 Mbps: no 1080p 8 Mbps
		7999:  "720p_4mbps,720p_2mbps,480p_1mbps",
		1500:  "480p_1mbps",
		500:   "480p_1mbps", // below the ladder: the lowest rung, as playback does
	}
	for capKbps, want := range cases {
		if got := strings.Join(allowed(capKbps), ","); got != want {
			t.Errorf("cap %d: got %s, want %s", capKbps, got, want)
		}
	}
}

func TestReadProgress(t *testing.T) {
	in := "frame=1\nout_time_us=2500000\nprogress=continue\nout_time_us=N/A\nout_time_us=5000000\nprogress=end\n"
	var p atomic.Int64
	readProgress(strings.NewReader(in), 10000, &p)
	if got := p.Load(); got != 500000 {
		t.Errorf("progress = %d millionths, want 500000", got)
	}
	// Past the duration caps just under 1; unknown duration leaves 0.
	p.Store(0)
	readProgress(strings.NewReader("out_time_us=99000000\n"), 10000, &p)
	if got := p.Load(); got != 990000 {
		t.Errorf("overrun progress = %d, want 990000", got)
	}
	p.Store(0)
	readProgress(strings.NewReader("out_time_us=1000\n"), 0, &p)
	if p.Load() != 0 {
		t.Error("unknown duration should leave progress at 0")
	}
}

func conv(size int64) gen.DownloadConversion {
	return gen.DownloadConversion{SizeBytes: &size}
}

func TestPickEvictions(t *testing.T) {
	ready := []gen.DownloadConversion{conv(40), conv(30), conv(20), conv(10)} // LRU order
	if got := pickEvictions(ready, 100, 0); got != nil {
		t.Error("no limit evicts nothing")
	}
	if got := pickEvictions(ready, 100, 100); got != nil {
		t.Error("at the limit evicts nothing")
	}
	if got := pickEvictions(ready, 100, 60); len(got) != 1 {
		t.Errorf("100 -> 60 should evict the oldest only, got %d", len(got))
	}
	if got := pickEvictions(ready, 100, 25); len(got) != 3 {
		t.Errorf("100 -> 25 should evict three, got %d", len(got))
	}
}

func TestTextSubtitleStreams(t *testing.T) {
	js := []byte(`[{"index":2,"codec":"subrip"},{"index":3,"codec":"hdmv_pgs_subtitle"},{"index":4,"codec":"ass"},{"codec":"subrip"}]`)
	got := textSubtitleStreams(js)
	if len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Errorf("got %v, want [2 4]", got)
	}
	if textSubtitleStreams(nil) != nil || textSubtitleStreams([]byte("nope")) != nil {
		t.Error("empty/bad JSON should give nothing")
	}
}

func TestConvertible(t *testing.T) {
	str := func(s string) *string { return &s }
	ok := media.File{Status: "active", VideoCodec: str("h264"), FilePath: "/nonexistent/movie.mkv"}
	if err := Convertible(&ok); err != nil {
		t.Fatalf("plain file refused: %v", err)
	}
	cases := map[string]media.File{
		"missing":  {Status: "missing", VideoCodec: str("h264")},
		"audio":    {Status: "active"},
		"damaged":  {Status: "active", VideoCodec: str("h264"), IntegrityStatus: media.IntegrityDamaged},
		"playlist": {Status: "active", VideoCodec: str("h264"), Container: str("hls")},
		"dovi p5":  {Status: "active", VideoCodec: str("hevc"), HDRType: str("dolby_vision")},
	}
	for name, f := range cases {
		f := f
		if err := Convertible(&f); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
}
