' Playback-mode decision for the Roku Video node. Mirrors the
' Android PlaybackHelper.decide() three-way split:
'
'   direct  — Roku can demux + decode the source as-is. Server
'             serves the file via /media/stream/{file.id} (no
'             transcode session, no extra CPU on the box).
'   remux   — video codec is fine but the container or audio isn't.
'             Server runs an ffmpeg session with video_copy=true so
'             the video stream copies (cheap) and only audio gets
'             re-encoded. Output is HLS.
'   transcode — full re-encode (HEVC source on a non-HEVC Roku,
'             unusual codecs like VC-1, etc.). Server picks the
'             quality + the encoder.
'
' The decision is intentionally conservative: anything we're not
' 100 % sure the Roku Video node can play directly falls into
' remux. Roku silently fails on unsupported codecs (black screen,
' no error event), so a remux fallback is always safer than
' "let's try and see."
'
' For video this is the fallback: the server's play decision
' (Playback_PlanFromServer below) is final when it answers. The
' X-Client-Capabilities header (Client_CapabilitiesHeaderFor) mirrors
' the container list here, so the server never direct-plays mkv or
' ts — change both. Its audio list deliberately adds ac3 / eac3:
' the server copies them into a remux (Dolby passthrough), and an
' mp4 + ac3 the server direct-plays uses that same decoder. This
' fallback stays conservative and leaves them out.

' Direct-play container set. mp4 / mov / m4v are the firmware-
' guaranteed wins on every Roku model since 2017. mkv direct play
' works on the same models but only when the inside streams are
' demuxer-compatible — keep it out of the direct list to avoid
' the "video plays without audio" failure mode that hits AC3
' inside MKV on some Roku TV firmwares.
function Playback_DirectContainers() as Object
    return ["mp4", "mov", "m4v"]
end function

' Direct-play video codec set. h264 is universal; hevc only when
' the device says it can decode it. Built dynamically by the
' caller via roDeviceInfo.CanDecodeVideo().
function Playback_DirectVideoCodecs(supportsHevc as Boolean) as Object
    out = ["h264"]
    if supportsHevc then out.push("hevc")
    return out
end function

' Direct-play audio codec set. aac is the safe-anywhere choice;
' ac3 / eac3 are widely supported on Roku TVs and Streaming Sticks
' but pre-2018 boxes need passthrough enabled at the OS level —
' keep them out of the direct list for the same reason mkv is
' excluded above. A server-side remux copies them (the capability
' header declares them) and re-encodes anything else to aac.
function Playback_DirectAudioCodecs() as Object
    return ["aac", "mp3"]
end function

' Remux-eligible video codec set. If the source uses one of these
' the server can copy the video stream into HLS without re-encoding,
' which is much cheaper server-side than a full transcode.
function Playback_RemuxVideoCodecs(supportsHevc as Boolean) as Object
    out = ["h264"]
    if supportsHevc then out.push("hevc")
    return out
end function

' Decide returns:
'   { mode: "direct" | "remux" | "transcode",
'     height: Integer,        ' target render height for transcode
'     videoCopy: Boolean,     ' server param: true → remux, false → full
'     supportsHevc: Boolean } ' server param: client capability flag
'
' Caller uses `mode` to pick the URL (direct file vs HLS playlist)
' and the other fields to populate the transcode start request.
'
' supportsHevc is taken as a parameter (rather than probed inside)
' so unit tests can drive both branches deterministically without
' shadowing roDeviceInfo. Production callers pass
' Playback_SupportsHevc() — see PlayerScene.brs.
function Playback_Decide(file as Object, supportsHevc as Boolean) as Object
    out = {
        mode: "transcode",
        height: 1080,
        videoCopy: false,
        supportsHevc: supportsHevc
    }

    if file = invalid then return out

    container = lcaseSafe(file.container)
    videoCodec = lcaseSafe(file.video_codec)
    audioCodec = lcaseSafe(file.audio_codec)

    ' Audio-only files (music tracks, audiobook chapters): always
    ' direct. Roku's audio decoders cover the full codec set we
    ' care about (mp3, aac, flac, alac, wav, ogg) without any
    ' container nuance worth gating.
    if videoCodec = ""
        out.mode = "direct"
        return out
    end if

    ' Dolby Vision is not supported. The server can't tonemap it correctly
    ' (libplacebo can't init on the deploy host — see docs/dolby-vision.md) and
    ' refuses with HTTP 415, so flag it here and skip the doomed transcode. The
    ' caller turns "unsupported" into a clear on-screen message.
    if lcaseSafe(file.hdr_type) = "dolby_vision"
        out.mode = "unsupported"
        return out
    end if

    ' Transcode height hint, set on every video verdict: a direct play or
    ' remux the server demotes to a transcode (Playback_FromVerdict) keeps
    ' it. Match source resolution: 4K source → 4K out, anything else →
    ' 1080p out. Server re-clamps against its own max-height cap and the
    ' viewer's quality profile, so this is just a hint.
    sourceH = 1080
    if file.resolution_h <> invalid and file.resolution_h > 0
        sourceH = file.resolution_h
    end if
    if sourceH >= 2160 then out.height = 2160 else out.height = 1080

    videoOk = inList(Playback_DirectVideoCodecs(supportsHevc), videoCodec)
    audioOk = audioCodec = "" or inList(Playback_DirectAudioCodecs(), audioCodec)
    containerOk = inList(Playback_DirectContainers(), container)

    if videoOk and audioOk and containerOk
        out.mode = "direct"
        return out
    end if

    if inList(Playback_RemuxVideoCodecs(supportsHevc), videoCodec)
        out.mode = "remux"
        out.videoCopy = true
        return out
    end if

    ' Full transcode at out.height.
    return out
end function

' A multi-part movie (cd1 + cd2, files[].part_index — Versions_StackOf)
' never direct-plays: one file is one part. A session started with part 1's
' file_id streams every part as one movie, so a direct verdict becomes a
' remux (the parts' video is copied); remux and transcode stand. Mirrors
' transcode.ForStack on the server.
function Playback_ForStack(decision as Object) as Object
    if decision.mode <> "direct" then return decision
    return {
        mode: "remux",
        height: decision.height,
        videoCopy: true,
        supportsHevc: decision.supportsHevc
    }
end function

' ── Server play decision (v2.6) ─────────────────────────────────────────
' Before a video plays, PlayerScene asks POST /items/{id}/playback-decision
' { file_id } and turns the answer (an ApiResult_From result) into a plan.
' The server's verdict is final, as on the other clients: it judged the
' file against this Roku's X-Client-Capabilities header (Client.brs, from
' roDeviceInfo — declaring the same direct-play limits as Playback_Decide
' above) plus what the device can't know (the viewer's quality profile,
' the internet bitrate cap, the integrity probe):
'
'   { kind: "limit", message }   429 TOO_MANY_SESSIONS: show message in a
'                                "Too many streams" dialog; don't play.
'   { kind: "optimized", url, token, fallback }
'                                reason "optimized_version": play
'                                optimized_version.stream_url directly (an
'                                MP4, H.264 / AAC) with stream_token as its
'                                ?token=; if it fails, play `fallback` once
'                                (the source's own verdict, source_decision).
'   { kind: "unplayable", message }
'                                unsupported (Dolby Vision) or damaged (the
'                                integrity probe found the bitstream
'                                undecodable): show message; don't play.
'   { kind: "play", decision }   directPlay → direct, directStream → remux,
'                                transcode → transcode (Playback_FromVerdict),
'                                in either direction: a local remux the
'                                server direct-plays becomes direct, a local
'                                direct it remuxes becomes remux. Keeps the
'                                local height hint and HEVC flag. A stack
'                                still never direct-plays (Playback_ForStack
'                                in PlayerScene's startPlayback).
'
' A failed call (older server without the endpoint, network, non-ok) or a
' verdict this build doesn't know plays the local decision as before.
' Depends on PlaybackStop.brs.
function Playback_PlanFromServer(result as Dynamic, localDecision as Object) as Object
    limitMsg = PlaybackLimit_FromResult(result)
    if limitMsg <> "" then return { kind: "limit", message: limitMsg }
    plan = { kind: "play", decision: localDecision }
    if result = invalid or type(result) <> "roAssociativeArray" or result.ok <> true then return plan
    d = result.data
    if d = invalid or type(d) <> "roAssociativeArray" then return plan

    ov = d.optimized_version
    if playbackStr(d.reason) = "optimized_version" and ov <> invalid and type(ov) = "roAssociativeArray" and playbackStr(ov.stream_url) <> ""
        fallback = Playback_FromVerdict(d.source_decision, localDecision)
        if fallback = invalid then fallback = localDecision
        return { kind: "optimized", url: ov.stream_url, token: playbackStr(ov.stream_token), fallback: fallback }
    end if

    verdict = playbackStr(d.decision)
    if verdict = "unsupported" then return { kind: "unplayable", message: Playback_UnsupportedMessage() }
    if verdict = "damaged" then return { kind: "unplayable", message: Playback_DamagedMessage() }
    serverDecision = Playback_FromVerdict(verdict, localDecision)
    if serverDecision <> invalid then plan.decision = serverDecision
    return plan
end function

' The one "unsupported" case today is Dolby Vision profile 5 — the server
' can't tonemap it (docs/dolby-vision.md). Shared with PlayerScene's local
' Dolby Vision gate so both say the same thing.
function Playback_UnsupportedMessage() as String
    return "Dolby Vision is not supported."
end function

' Worded like the other clients: nothing can play an undecodable bitstream,
' so there's no point retrying.
function Playback_DamagedMessage() as String
    return "This file is damaged and can't be played."
end function

' A server verdict (directPlay / directStream / transcode) as this player's
' decision: direct, remux (video copy) or a full transcode at the local
' decision's height. invalid for anything else (unsupported and damaged,
' which Playback_PlanFromServer refuses first; missing; unknown) — the
' caller keeps its own decision.
function Playback_FromVerdict(verdict as Dynamic, localDecision as Object) as Dynamic
    v = playbackStr(verdict)
    out = {
        mode: "",
        height: localDecision.height,
        videoCopy: false,
        supportsHevc: localDecision.supportsHevc
    }
    if v = "directPlay"
        out.mode = "direct"
    else if v = "directStream"
        out.mode = "remux"
        out.videoCopy = true
    else if v = "transcode"
        out.mode = "transcode"
    else
        return invalid
    end if
    return out
end function

function playbackStr(v as Dynamic) as String
    if v = invalid then return ""
    if type(v) <> "String" and type(v) <> "roString" then return ""
    return v
end function

' Probe the Roku firmware once to learn whether the device can
' hardware-decode HEVC. Most 4K Roku models from 2017+ can; older
' Roku Express / Stick boxes can't. Cached on `m.global` so we
' don't re-query on every play.
function Playback_SupportsHevc() as Boolean
    info = CreateObject("roDeviceInfo")
    if info = invalid then return false
    if info.CanDecodeVideo <> invalid
        result = info.CanDecodeVideo({ Codec: "hevc" })
        if result <> invalid and result.Result = true then return true
        return false
    end if
    return false
end function

function lcaseSafe(s as Dynamic) as String
    if s = invalid then return ""
    return LCase(s)
end function

function inList(list as Object, needle as String) as Boolean
    if list = invalid then return false
    for each item in list
        if item = needle then return true
    end for
    return false
end function
