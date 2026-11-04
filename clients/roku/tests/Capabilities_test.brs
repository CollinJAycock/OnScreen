' Unit tests for Client_CapabilitiesHeaderFor in source/api/Client.brs —
' the X-Client-Capabilities grammar (docs/capability-profiles.md) the server
' parses in internal/transcode/capability.go.

sub Main()
    full = { hevc: true, hevc10: true, uhd: true, hdr10: true, hlg: true }
    runCase("header: 4K HDR Roku on an HDR TV", Client_CapabilitiesHeaderFor(full), "videoDecoder=h264:h265,audioDecoder=aac:mp3:ac3:eac3,protocols=mp4,maxWidth=3840,maxHeight=2160,maxAudioChannels=2,maxbitdepth=10,hdr=1,hlg=1,optimized=1")
    hd = { hevc: false, hevc10: false, uhd: false, hdr10: false, hlg: false }
    runCase("header: HD H.264-only Roku", Client_CapabilitiesHeaderFor(hd), "videoDecoder=h264,audioDecoder=aac:mp3:ac3:eac3,protocols=mp4,maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0,optimized=1")
    ' A 4K Roku on a 4K TV without HDR: 4K and 10-bit HEVC, but HDR is
    ' tone-mapped by the server.
    sdr4k = { hevc: true, hevc10: true, uhd: true, hdr10: false, hlg: false }
    runCase("header: 4K Roku on an SDR TV", Client_CapabilitiesHeaderFor(sdr4k), "videoDecoder=h264:h265,audioDecoder=aac:mp3:ac3:eac3,protocols=mp4,maxWidth=3840,maxHeight=2160,maxAudioChannels=2,maxbitdepth=10,hdr=0,optimized=1")
    ' An HDR10 display that doesn't take HLG: HLG is tone-mapped, HDR10 isn't.
    nohlg = { hevc: true, hevc10: true, uhd: true, hdr10: true, hlg: false }
    runBool("header: HDR10 without HLG says hlg=0", Instr(1, Client_CapabilitiesHeaderFor(nohlg), ",hdr=1,hlg=0,") > 0, true)
    ' The display says HDR but the decoder has no Main 10: no HDR claim.
    no10 = { hevc: true, hevc10: false, uhd: true, hdr10: true, hlg: true }
    runBool("header: HDR needs Main 10", Instr(1, Client_CapabilitiesHeaderFor(no10), ",maxbitdepth=8,hdr=0,") > 0, true)
    ' A 4K video mode on a Roku without HEVC: still 4K (H.264), 8-bit.
    avc4k = { hevc: false, hevc10: false, uhd: true, hdr10: false, hlg: false }
    runBool("header: 4K output without HEVC stays 8-bit", Instr(1, Client_CapabilitiesHeaderFor(avc4k), ",maxWidth=3840,maxHeight=2160,maxAudioChannels=2,maxbitdepth=8,") > 0, true)
    runBool("header: declares optimized versions", Instr(1, Client_CapabilitiesHeaderFor(hd), ",optimized=1") > 0, true)
    ' The server's play decision is final, so the header claims no container
    ' Playback_Decide won't direct-play: no mkv / ts.
    runBool("header: mp4 is the only direct-play container", Instr(1, Client_CapabilitiesHeaderFor(full), ",protocols=mp4,") > 0, true)

    runBool("IsTrue: true", Client_IsTrue(true), true)
    runBool("IsTrue: false", Client_IsTrue(false), false)
    runBool("IsTrue: missing field", Client_IsTrue(invalid), false)
    runBool("IsTrue: a string", Client_IsTrue("true"), false)

    ' The Streaming Stick 4K on an LG C1 + receiver: stereo AAC decoder, AC-3 /
    ' E-AC-3 passthrough. The server lifts the channel cap for the
    ' passthrough codecs only.
    stick = { hevc: true, hevc10: true, uhd: true, hdr10: true, hlg: true, aacChannels: 2, passthrough: ["ac3", "eac3"] }
    runCase("header: Streaming Stick 4K with a receiver", Client_CapabilitiesHeaderFor(stick), "videoDecoder=h264:h265,audioDecoder=aac:mp3:ac3:eac3,protocols=mp4,maxWidth=3840,maxHeight=2160,maxAudioChannels=2,maxbitdepth=10,hdr=1,hlg=1,audioPassthrough=ac3:eac3,optimized=1")
    sixAac = { hevc: false, hevc10: false, uhd: false, hdr10: false, hlg: false, aacChannels: 6, passthrough: [] }
    runBool("header: a 5.1 AAC decoder says 6", Instr(1, Client_CapabilitiesHeaderFor(sixAac), ",maxAudioChannels=6,") > 0, true)
    runBool("header: no passthrough, no key", Instr(1, Client_CapabilitiesHeaderFor(sixAac), "audioPassthrough") = 0, true)
    onlyAc3 = { hevc: false, hevc10: false, uhd: false, hdr10: false, hlg: false, aacChannels: 2, passthrough: ["ac3"] }
    runBool("header: AC-3 passthrough alone", Instr(1, Client_CapabilitiesHeaderFor(onlyAc3), ",audioPassthrough=ac3,") > 0, true)

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
