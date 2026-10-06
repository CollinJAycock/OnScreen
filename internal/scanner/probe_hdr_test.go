package scanner

import (
	"encoding/json"
	"testing"
)

// Side data as ffprobe prints it for a stream with a Dolby Vision
// configuration record (the records below are copied from real releases).
func dvRecord(profile, compat int) string {
	b, _ := json.Marshal(map[string]any{
		"side_data_type":                "DOVI configuration record",
		"dv_version_major":              1,
		"dv_version_minor":              0,
		"dv_profile":                    profile,
		"dv_level":                      6,
		"rpu_present_flag":              1,
		"el_present_flag":               0,
		"bl_present_flag":               1,
		"dv_bl_signal_compatibility_id": compat,
		"dv_md_compression":             "none",
	})
	return string(b)
}

const contentLight = `{"side_data_type": "Content light level metadata", "max_content": 1000, "max_average": 400}`

func TestDetectHDR(t *testing.T) {
	cases := []struct {
		name        string
		stream      string
		wantHDR     string // "" = SDR (nil)
		wantProfile int    // 0 = no Dolby Vision record
	}{
		{"SDR", `{"color_transfer": "bt709"}`, "", 0},
		{"no signalling at all", `{}`, "", 0},
		{"HDR10 by transfer", `{"color_transfer": "smpte2084"}`, "hdr10", 0},
		{"HDR10 by content light level alone", `{"side_data_list": [` + contentLight + `]}`, "hdr10", 0},
		{"HLG", `{"color_transfer": "arib-std-b67"}`, "hlg", 0},
		// The transfer decides before content light level: HLG that also
		// carries a MaxCLL is still HLG (it used to read as HDR10).
		{"HLG with content light level", `{"color_transfer": "arib-std-b67", "side_data_list": [` + contentLight + `]}`, "hlg", 0},

		// Profile 7, UHD Blu-ray remux: HDR10 base layer (compatibility 6).
		// Alien: Romulus carries the record and no content light level, so
		// it used to read as "dolby_vision" and every client refused it.
		{"profile 7 over HDR10", `{"color_transfer": "smpte2084", "side_data_list": [` + dvRecord(7, 6) + `]}`, "hdr10", 7},
		// Profile 8.1 (The Crow): HDR10 base layer.
		{"profile 8.1 over HDR10", `{"color_transfer": "smpte2084", "side_data_list": [` + dvRecord(8, 1) + `, ` + contentLight + `]}`, "hdr10", 8},
		// Profile 8.4: HLG base layer.
		{"profile 8.4 over HLG", `{"color_transfer": "arib-std-b67", "side_data_list": [` + dvRecord(8, 4) + `]}`, "hlg", 8},
		// Profile 8 record over BT.709 video with no Dolby Vision data in it
		// (Hellraiser, The Sound of Music): SDR, whatever the record says.
		{"profile 8 record over SDR video", `{"color_transfer": "bt709", "side_data_list": [` + dvRecord(8, 1) + `]}`, "", 8},
		// Profile 9 (AVC) over SDR.
		{"profile 9 over SDR", `{"color_transfer": "bt709", "side_data_list": [` + dvRecord(9, 2) + `]}`, "", 9},

		// Profile 5 has no compatible base layer: refused, whatever the
		// stream's own transfer claims.
		{"profile 5", `{"side_data_list": [` + dvRecord(5, 0) + `]}`, "dolby_vision", 5},
		{"profile 5 tagged PQ", `{"color_transfer": "smpte2084", "side_data_list": [` + dvRecord(5, 0) + `]}`, "dolby_vision", 5},
		{"compatibility 0 on another profile", `{"color_transfer": "smpte2084", "side_data_list": [` + dvRecord(8, 0) + `]}`, "dolby_vision", 8},

		// A record without a compatibility id: the stream's own signalling
		// decides, and with none there is nothing to fall back to.
		{"record without compatibility id over PQ", `{"color_transfer": "smpte2084", "side_data_list": [{"side_data_type": "DOVI configuration record", "dv_profile": 8}]}`, "hdr10", 8},
		{"record without compatibility id or signalling", `{"side_data_list": [{"side_data_type": "DOVI configuration record", "dv_profile": 8}]}`, "dolby_vision", 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s ffprobeStream
			if err := json.Unmarshal([]byte(c.stream), &s); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			hdr, profile := detectHDR(&s)
			gotHDR := ""
			if hdr != nil {
				gotHDR = *hdr
			}
			if gotHDR != c.wantHDR {
				t.Errorf("hdr = %q, want %q", gotHDR, c.wantHDR)
			}
			gotProfile := 0
			if profile != nil {
				gotProfile = *profile
			}
			if gotProfile != c.wantProfile {
				t.Errorf("dv profile = %d, want %d", gotProfile, c.wantProfile)
			}
		})
	}
}

// The profile is copied, not aliased into the probe output.
func TestDetectHDR_ProfileIsACopy(t *testing.T) {
	var s ffprobeStream
	if err := json.Unmarshal([]byte(`{"color_transfer": "smpte2084", "side_data_list": [`+dvRecord(7, 6)+`]}`), &s); err != nil {
		t.Fatal(err)
	}
	_, profile := detectHDR(&s)
	*s.SideDataList[0].DVProfile = 99
	if *profile != 7 {
		t.Fatalf("profile = %d after the stream changed, want 7", *profile)
	}
}
