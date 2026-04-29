' Unit tests for Playback_Decide in source/playback/Decide.brs.
'
' Decide takes supportsHevc as a parameter (rather than probing
' roDeviceInfo internally) so we can drive both branches from the
' Node-side brs interpreter without needing to stub the firmware
' API. Production callers pass Playback_SupportsHevc(); tests pass
' true/false directly.
'
' Mirrors the Android PlaybackHelperTest matrix so a future codec
' rule change keeps both clients in sync.

sub Main()
    runCase("h264/aac/mp4 → direct", { container: "mp4", video_codec: "h264", audio_codec: "aac" }, false, "direct", false)
    runCase("h264/mp3/mov → direct", { container: "mov", video_codec: "h264", audio_codec: "mp3" }, false, "direct", false)
    runCase("hevc/aac/mp4 with hevc support → direct", { container: "mp4", video_codec: "hevc", audio_codec: "aac" }, true, "direct", false)

    runCase("hevc/aac/mp4 without hevc support → transcode", { container: "mp4", video_codec: "hevc", audio_codec: "aac", resolution_h: 1080 }, false, "transcode", false)
    runCase("h264/dts/mp4 → remux (audio incompatible)", { container: "mp4", video_codec: "h264", audio_codec: "dts" }, false, "remux", true)
    runCase("h264/aac/mkv → remux (container incompatible)", { container: "matroska", video_codec: "h264", audio_codec: "aac" }, false, "remux", true)
    runCase("h264/truehd/mkv → remux (audio + container incompatible)", { container: "matroska", video_codec: "h264", audio_codec: "truehd" }, false, "remux", true)
    runCase("hevc/truehd/mkv with hevc support → remux", { container: "matroska", video_codec: "hevc", audio_codec: "truehd" }, true, "remux", true)

    runCase("vc1/mp3/mp4 → transcode (vc1 not in remux list)", { container: "mp4", video_codec: "vc1", audio_codec: "mp3", resolution_h: 1080 }, false, "transcode", false)
    runCase("mpeg2video/aac/ts → transcode", { container: "ts", video_codec: "mpeg2video", audio_codec: "aac", resolution_h: 1080 }, false, "transcode", false)

    runCase("audio-only flac → direct (no video codec)", { container: "flac", video_codec: invalid, audio_codec: "flac" }, false, "direct", false)

    ' 4K source produces height=2160 hint
    runCase4K("hevc 4k without hevc support → transcode at 2160", { container: "matroska", video_codec: "hevc", audio_codec: "aac", resolution_h: 2160 }, false, "transcode", 2160)
    runCase4K("vc1 1080 → transcode at 1080", { container: "ts", video_codec: "vc1", audio_codec: "aac", resolution_h: 1080 }, false, "transcode", 1080)

    print "DONE: PlaybackDecide_test"
end sub

sub runCase(name as String, file as Object, hevc as Boolean, wantMode as String, wantVideoCopy as Boolean)
    decision = Playback_Decide(file, hevc)
    if decision.mode <> wantMode
        print "FAIL: " + name + " — mode got=" + decision.mode + " want=" + wantMode
        return
    end if
    if decision.videoCopy <> wantVideoCopy
        wantStr = "false"
        gotStr = "false"
        if wantVideoCopy then wantStr = "true"
        if decision.videoCopy then gotStr = "true"
        print "FAIL: " + name + " — videoCopy got=" + gotStr + " want=" + wantStr
        return
    end if
    print "PASS: " + name
end sub

sub runCase4K(name as String, file as Object, hevc as Boolean, wantMode as String, wantHeight as Integer)
    decision = Playback_Decide(file, hevc)
    if decision.mode <> wantMode
        print "FAIL: " + name + " — mode got=" + decision.mode + " want=" + wantMode
        return
    end if
    if decision.height <> wantHeight
        print "FAIL: " + name + " — height got=" + decision.height.ToStr() + " want=" + wantHeight.ToStr()
        return
    end if
    print "PASS: " + name
end sub
