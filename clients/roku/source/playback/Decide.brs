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
' excluded above. Server-side remux re-encodes to aac.
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

' ── Server play decision (v2.6) ─────────────────────────────────────────
' Before a video plays, PlayerScene asks POST /items/{id}/playback-decision
' { file_id } and turns the answer (an ApiResult_From result) into a plan
' on top of the local decision:
'
'   { kind: "limit", message }   429 TOO_MANY_SESSIONS: show message in a
'                                "Too many streams" dialog; don't play.
'   { kind: "optimized", url, token, fallback }
'                                reason "optimized_version": play
'                                optimized_version.stream_url directly (an
'                                MP4, H.264 / AAC) with stream_token as its
'                                ?token=; if it fails, play `fallback` once
'                                (the source's own verdict, source_decision).
'   { kind: "play", decision }   anything else: the local decision — demoted
'                                to a transcode when the server says the
'                                source must transcode (the viewer's quality
'                                profile, a bitrate cap, a resolution
'                                ceiling). The server never upgrades it.
'
' A failed call (older server without the endpoint, network) plays the
' local decision as before. Depends on PlaybackStop.brs.
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

    if playbackStr(d.decision) = "transcode" and (localDecision.mode = "direct" or localDecision.mode = "remux")
        plan.decision = Playback_FromVerdict("transcode", localDecision)
    end if
    return plan
end function

' A server verdict (directPlay / directStream / transcode) as this player's
' decision: direct, remux (video copy) or a full transcode at the local
' decision's height. invalid for anything else (unsupported, damaged,
' missing) — the caller keeps its own decision.
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
