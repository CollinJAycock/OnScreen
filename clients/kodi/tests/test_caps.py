from resources.lib.onscreen import caps


def parse(header):
    out = {}
    for part in header.split(","):
        k, v = part.split("=", 1)
        out[k] = v
    return out


def test_default_header_is_wide():
    h = parse(caps.header(caps.DeviceCaps()))
    video = h["videoDecoder"].split(":")
    assert video[:4] == ["h264", "h265", "av1", "vp9"]
    assert "mpeg2video" in video and "vc1" in video
    audio = h["audioDecoder"].split(":")
    for codec in ("aac", "ac3", "eac3", "dts", "truehd", "flac", "opus", "pcm_s24le"):
        assert codec in audio
    assert h["protocols"].split(":")[:4] == ["mkv", "mp4", "ts", "avi"]
    assert (h["maxWidth"], h["maxHeight"]) == ("3840", "2160")
    # Kodi downmixes itself: never ask the server to.
    assert h["maxAudioChannels"] == "8"
    assert h["maxbitdepth"] == "10" and h["vp9MaxBitDepth"] == "10"
    assert h["hdr"] == "0" and "hlg" not in h and "dovi" not in h and "audioPassthrough" not in h
    assert h["optimized"] == "1" and h["vodRemux"] == "1"


def test_codec_switches_and_resolution():
    h = parse(caps.header(caps.DeviceCaps(max_width=1920, max_height=1080, hevc=False, av1=False, vp9=False)))
    assert h["videoDecoder"].split(":")[0] == "h264"
    assert "h265" not in h["videoDecoder"] and "av1" not in h["videoDecoder"] and "vp9" not in h["videoDecoder"]
    assert h["maxbitdepth"] == "8" and "vp9MaxBitDepth" not in h
    assert (h["maxWidth"], h["maxHeight"]) == ("1920", "1080")


def test_hdr_dovi_and_passthrough():
    h = parse(
        caps.header(
            caps.DeviceCaps(hdr10=True, hlg=False, dovi=("5", "8.1", "8.4", "7"), passthrough=("eac3", "truehd", "ac3"))
        )
    )
    assert h["hdr"] == "1" and h["hlg"] == "0"
    assert h["dovi"] == "5:8.1:8.4"  # profile 7 isn't claimed
    # Only the codecs that ride MPEG-TS segments.
    assert h["audioPassthrough"] == "ac3:eac3"


def test_parse_hdr_types():
    assert caps.parse_hdr_types("HDR10, HLG, Dolby Vision") == (True, True, True)
    assert caps.parse_hdr_types("hdr10") == (True, False, False)
    assert caps.parse_hdr_types("") == (False, False, False)
