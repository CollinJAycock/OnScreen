' Unit tests for Client_CapabilitiesHeaderFor in source/api/Client.brs —
' the X-Client-Capabilities grammar (docs/capability-profiles.md) the server
' parses in internal/transcode/capability.go.

sub Main()
    runCase("header: hevc device", Client_CapabilitiesHeaderFor(true), "videoDecoder=h264:h265,audioDecoder=aac:mp3:ac3:eac3,protocols=mp4,maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1,optimized=1")
    runCase("header: h264-only device", Client_CapabilitiesHeaderFor(false), "videoDecoder=h264,audioDecoder=aac:mp3:ac3:eac3,protocols=mp4,maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1,optimized=1")
    runBool("header: declares optimized versions", Instr(1, Client_CapabilitiesHeaderFor(false), ",optimized=1") > 0, true)
    ' The server's play decision is final, so the header claims no container
    ' Playback_Decide won't direct-play: no mkv / ts.
    runBool("header: mp4 is the only direct-play container", Instr(1, Client_CapabilitiesHeaderFor(true), ",protocols=mp4,") > 0, true)

    print "DONE: Capabilities_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
