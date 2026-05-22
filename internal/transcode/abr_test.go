package transcode

import (
	"strings"
	"testing"
)

func heights(rends []Rendition) []int {
	out := make([]int, len(rends))
	for i, r := range rends {
		out[i] = r.Height
	}
	return out
}

func TestBuildLadder_CapsAtSourceHeight(t *testing.T) {
	// 1080p source → no 2160/1440 rungs; top rung is 1080.
	rends := BuildLadder(1920, 1080, 0, false, 0)
	got := heights(rends)
	want := []int{1080, 720, 480, 360}
	if len(got) != len(want) {
		t.Fatalf("heights = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("heights = %v, want %v", got, want)
		}
	}
	// Highest-first ordering.
	for i := 1; i < len(rends); i++ {
		if rends[i].Height >= rends[i-1].Height {
			t.Errorf("not sorted highest-first: %v", got)
		}
	}
}

func TestBuildLadder_4KIncludesTopRungs(t *testing.T) {
	rends := BuildLadder(3840, 2160, 0, false, 0)
	if rends[0].Height != 2160 {
		t.Errorf("4K source top rung = %dp, want 2160p", rends[0].Height)
	}
	if rends[0].BitrateKbps != 40000 {
		t.Errorf("2160p bitrate = %d, want 40000", rends[0].BitrateKbps)
	}
}

func TestBuildLadder_OperatorCap(t *testing.T) {
	// Operator pins the ceiling at 720p on a 4K source.
	rends := BuildLadder(3840, 2160, 0, false, 720)
	if rends[0].Height != 720 {
		t.Errorf("with cap=720, top rung = %dp, want 720p", rends[0].Height)
	}
	for _, r := range rends {
		if r.Height > 720 {
			t.Errorf("cap=720 but got rung %dp", r.Height)
		}
	}
}

func TestBuildLadder_HEVCScalesBitrate(t *testing.T) {
	h264 := BuildLadder(1920, 1080, 0, false, 0)
	hevc := BuildLadder(1920, 1080, 0, true, 0)
	if hevc[0].BitrateKbps >= h264[0].BitrateKbps {
		t.Errorf("HEVC 1080p bitrate %d should be below H.264 %d", hevc[0].BitrateKbps, h264[0].BitrateKbps)
	}
	if hevc[0].BitrateKbps != ScaleBitrateForHEVC(8000) {
		t.Errorf("HEVC 1080p bitrate = %d, want %d", hevc[0].BitrateKbps, ScaleBitrateForHEVC(8000))
	}
}

func TestBuildLadder_NeverExceedsSourceBitrate(t *testing.T) {
	// A low-bitrate 1080p source (3 Mbps) shouldn't advertise an 8 Mbps rung.
	rends := BuildLadder(1920, 1080, 3000, false, 0)
	for _, r := range rends {
		if r.BitrateKbps > 3000 {
			t.Errorf("rung %s bitrate %d exceeds source 3000", r.Label, r.BitrateKbps)
		}
	}
}

func TestBuildLadder_TinySourceCollapsesToOne(t *testing.T) {
	// 240p source: below every standard rung → single source-res rendition.
	rends := BuildLadder(426, 240, 0, false, 0)
	if len(rends) != 1 {
		t.Fatalf("tiny source produced %d rungs, want 1: %v", len(rends), heights(rends))
	}
	if rends[0].Height != 240 {
		t.Errorf("tiny source rung = %dp, want 240p", rends[0].Height)
	}
}

func TestBuildLadder_WidthEvenAndAspectPreserved(t *testing.T) {
	// 2.39:1 cinemascope 1080p (2560x1072-ish) — widths must stay even.
	rends := BuildLadder(2560, 1072, 0, false, 0)
	for _, r := range rends {
		if r.Width%2 != 0 {
			t.Errorf("rung %s width %d is odd", r.Label, r.Width)
		}
	}
}

func TestBuildMasterPlaylist(t *testing.T) {
	rends := BuildLadder(1920, 1080, 0, false, 0)
	master := BuildMasterPlaylist(rends, "", func(r Rendition) string {
		return r.Label + "/playlist.m3u8"
	})

	if !strings.HasPrefix(master, "#EXTM3U\n") {
		t.Errorf("master must start with #EXTM3U:\n%s", master)
	}
	// One STREAM-INF + one URL per rendition.
	if n := strings.Count(master, "#EXT-X-STREAM-INF:"); n != len(rends) {
		t.Errorf("got %d STREAM-INF lines, want %d", n, len(rends))
	}
	if !strings.Contains(master, "RESOLUTION=1920x1080") {
		t.Errorf("master missing 1080p resolution:\n%s", master)
	}
	if !strings.Contains(master, "1080p/playlist.m3u8") {
		t.Errorf("master missing 1080p variant URL:\n%s", master)
	}
	// BANDWIDTH includes the audio allowance, in bits/s.
	wantBW := (8000 + audioAllowanceKbps) * 1000
	if !strings.Contains(master, "BANDWIDTH="+itoa(wantBW)) {
		t.Errorf("master missing expected BANDWIDTH=%d:\n%s", wantBW, master)
	}
	// No CODECS attribute when none is supplied (H.264 players probe fine).
	if strings.Contains(master, "CODECS=") {
		t.Errorf("H.264 master should omit CODECS:\n%s", master)
	}
}

func TestBuildMasterPlaylist_HEVCCodecs(t *testing.T) {
	rends := BuildLadder(3840, 2160, 0, true, 0)
	codecs := HEVCMasterCodecs(rends[0].Height)
	master := BuildMasterPlaylist(rends, codecs, func(r Rendition) string {
		return r.Label + "/index.m3u8"
	})

	// Every variant carries the HEVC + AAC CODECS attribute.
	n := strings.Count(master, "CODECS=\""+codecs+"\"")
	if n != len(rends) {
		t.Errorf("got %d CODECS attrs, want %d:\n%s", n, len(rends), master)
	}
	if !strings.HasPrefix(codecs, "hvc1.") || !strings.Contains(codecs, "mp4a.40.2") {
		t.Errorf("HEVC codecs string looks wrong: %q", codecs)
	}
	// 4K ladder → level 5.1 (L153).
	if !strings.Contains(codecs, "L153") {
		t.Errorf("4K HEVC codecs should advertise L153, got %q", codecs)
	}
}

// itoa avoids pulling strconv just for the one assertion above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
