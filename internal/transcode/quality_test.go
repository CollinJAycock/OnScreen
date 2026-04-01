package transcode

import "testing"

func TestSelectQuality_ServerCapsApply(t *testing.T) {
	serverCaps := ServerCaps{MaxBitrateKbps: 8000, MaxWidth: 1920, MaxHeight: 1080}
	q := SelectQuality(0, 0, 0, 0, 0, serverCaps)
	if q.Bitrate != 8000 {
		t.Errorf("want bitrate 8000, got %d", q.Bitrate)
	}
	if q.MaxWidth != 1920 {
		t.Errorf("want width 1920, got %d", q.MaxWidth)
	}
}

func TestSelectQuality_ClientConstrainsServer(t *testing.T) {
	serverCaps := ServerCaps{MaxBitrateKbps: 40000, MaxWidth: 3840, MaxHeight: 2160}
	// Client requests 4 Mbps 720p.
	q := SelectQuality(4000, 1280, 720, 0, 0, serverCaps)
	if q.Bitrate != 4000 {
		t.Errorf("want bitrate 4000 (client limit), got %d", q.Bitrate)
	}
	if q.MaxWidth != 1280 {
		t.Errorf("want width 1280 (client limit), got %d", q.MaxWidth)
	}
	if q.MaxHeight != 720 {
		t.Errorf("want height 720 (client limit), got %d", q.MaxHeight)
	}
}

func TestSelectQuality_ServerConstrainsClient(t *testing.T) {
	serverCaps := ServerCaps{MaxBitrateKbps: 4000, MaxWidth: 1280, MaxHeight: 720}
	// Client wants 40 Mbps 4K — server cap wins.
	q := SelectQuality(40000, 3840, 2160, 0, 0, serverCaps)
	if q.Bitrate != 4000 {
		t.Errorf("want bitrate 4000 (server cap), got %d", q.Bitrate)
	}
	if q.MaxWidth != 1280 {
		t.Errorf("want width 1280 (server cap), got %d", q.MaxWidth)
	}
}

func TestSelectQuality_NoUpscaleBeyondSource(t *testing.T) {
	serverCaps := ServerCaps{MaxBitrateKbps: 40000, MaxWidth: 3840, MaxHeight: 2160}
	// Source is 720p — should not upscale to 1080p even if both server and client allow it.
	q := SelectQuality(0, 0, 0, 1280, 720, serverCaps)
	if q.MaxWidth != 1280 {
		t.Errorf("want width 1280 (source cap), got %d", q.MaxWidth)
	}
	if q.MaxHeight != 720 {
		t.Errorf("want height 720 (source cap), got %d", q.MaxHeight)
	}
}

func TestSelectQuality_ZeroServerCapsDefaultToMax(t *testing.T) {
	serverCaps := ServerCaps{} // zero = no limits
	q := SelectQuality(0, 0, 0, 0, 0, serverCaps)
	if q.Bitrate != 20000 {
		t.Errorf("want default bitrate 20000, got %d", q.Bitrate)
	}
	if q.MaxWidth != 3840 {
		t.Errorf("want default width 3840, got %d", q.MaxWidth)
	}
	if q.MaxHeight != 2160 {
		t.Errorf("want default height 2160, got %d", q.MaxHeight)
	}
}

func TestSelectQuality_MinOfAllConstraints(t *testing.T) {
	serverCaps := ServerCaps{MaxBitrateKbps: 8000, MaxWidth: 1920, MaxHeight: 1080}
	// Source is 480p, client requests 720p, server allows 1080p — source wins.
	q := SelectQuality(2000, 1280, 720, 854, 480, serverCaps)
	if q.MaxWidth != 854 {
		t.Errorf("want width 854 (source), got %d", q.MaxWidth)
	}
	if q.MaxHeight != 480 {
		t.Errorf("want height 480 (source), got %d", q.MaxHeight)
	}
	if q.Bitrate != 2000 {
		t.Errorf("want bitrate 2000 (client), got %d", q.Bitrate)
	}
}
