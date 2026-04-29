' PlayerScene controller. itemId is set by the caller before mount;
' on init we fire ItemFetchTask, then route through Playback_Decide
' to pick one of three paths:
'
'   direct  → file is roku-friendly (mp4 / h264 / aac etc.); hand
'             /media/stream/{file.id} straight to the Video node.
'   remux   → video copy + audio re-encode via the server's ffmpeg
'             session. Output is HLS; we POST /items/{id}/transcode
'             with video_copy=true and feed the playlist URL into
'             the Video node with streamFormat=hls.
'   transcode → full re-encode. Same code path as remux but
'             video_copy=false; server picks bitrate + encoder.
'
' Direct play uses the per-file stream_token (24 h, file_id-bound)
' so a long movie doesn't die at the 1 h access-token expiry.
' Transcode + remux use the per-session token the server returns
' alongside the playlist URL.
'
' Cleanup: when the scene is left (back, EOS, error), if a transcode
' session was started we DELETE it so the server doesn't leave the
' ffmpeg process running until its idle-timeout sweep.

sub init()
    m.video = m.top.findNode("video")
    m.itemTask = m.top.findNode("itemTask")
    m.transcodeTask = m.top.findNode("transcodeTask")
    m.markersTask = m.top.findNode("markersTask")
    m.childrenTask = m.top.findNode("childrenTask")
    m.skipMarker = m.top.findNode("skipMarker")
    m.upNext = m.top.findNode("upNext")
    m.upNextTitle = m.top.findNode("upNextTitle")
    m.upNextHint = m.top.findNode("upNextHint")
    m.syncTimer = m.top.findNode("syncTimer")
    m.trackPicker = m.top.findNode("trackPicker")
    m.trackPickerTitle = m.top.findNode("trackPickerTitle")
    m.trackPickerList = m.top.findNode("trackPickerList")

    ' Track-picker state. trackPickerMode is "" (closed), "audio",
    ' or "subtitle". The * (options) button cycles closed → audio
    ' → subtitle → closed; up/down navigates the LabelList; OK
    ' selects; back closes. Active indices track what the current
    ' transcode session was started for (audio_stream_index) and
    ' what the firmware Video node's subtitle selector is set to.
    ' -1 on subtitle means "off".
    m.trackPickerMode = ""
    m.activeAudioIndex = 0
    m.activeSubtitleIndex = -1
    ' Cache the transcode inputs needed for an audio re-issue. Set
    ' once when the first transcode session lands; nil on direct
    ' play (the firmware Video node's audio track selector sees
    ' every track in the container, so re-issue isn't needed).
    m.lastTranscodeRequest = invalid

    m.session = invalid ' { session_id, token } when remux/transcode
    m.item = invalid
    m.file = invalid

    ' Markers + Up Next state. Markers come from /items/{id}/markers
    ' on init; the active one is recomputed on every position tick.
    ' dismissedMarkers prevents a skipped marker from re-popping if
    ' the user scrubs back into it.
    m.markers = []
    m.activeMarker = invalid
    m.dismissedMarkers = {}
    ' Lead window before EOS at which the Up Next overlay surfaces,
    ' and the countdown that runs once it's visible. Mirrors the
    ' Android PlaybackFragment defaults.
    m.UP_NEXT_LEAD_MS = 25000
    m.upNextShown = false
    m.nextSibling = invalid

    ' Cross-device sync: track the last position we reported via
    ' /progress so we can ignore self-loop echoes on the polled
    ' item-detail re-fetch.
    m.lastReportedPositionMs = -1

    m.itemTask.observeField("state", "onItemTaskState")
    m.transcodeTask.observeField("state", "onTranscodeTaskState")
    m.markersTask.observeField("state", "onMarkersTaskState")
    m.childrenTask.observeField("state", "onChildrenTaskState")
    m.video.observeField("state", "onVideoState")
    m.video.observeField("position", "onVideoPosition")
    m.syncTimer.observeField("fire", "onSyncTimerFire")
    m.trackPickerList.observeField("itemSelected", "onTrackPickerSelect")

    if m.top.itemId = invalid or m.top.itemId = ""
        bailToHome()
        return
    end if

    m.itemTask.itemId = m.top.itemId
    m.itemTask.control = "RUN"
end sub

sub onItemTaskState()
    if m.itemTask.state <> "done" then return
    item = m.itemTask.result
    if item = invalid or item.files = invalid or item.files.Count() = 0
        bailToHome()
        return
    end if
    m.item = item
    m.file = item.files[0]

    ' Fire markers + children fetches in parallel — neither blocks
    ' the playback start. Markers come back as an array (often
    ' empty for non-episodes); children may be empty for movies /
    ' standalone tracks, which the code paths handle gracefully.
    m.markersTask.path = "/api/v1/items/" + m.top.itemId + "/markers"
    m.markersTask.control = "RUN"

    if item.parent_id <> invalid and item.parent_id <> ""
        m.childrenTask.path = ApiItemChildren(item.parent_id)
        m.childrenTask.control = "RUN"
    end if

    ' Cross-device sync timer kicks now and stays running for the
    ' life of the scene; the tick handler short-circuits when the
    ' player is actively playing locally.
    m.syncTimer.control = "start"

    decision = Playback_Decide(m.file, Playback_SupportsHevc())

    serverUrl = Prefs_GetServerUrl()
    if serverUrl = invalid
        bailToHome()
        return
    end if

    if decision.mode = "direct"
        startDirectPlayback(serverUrl)
        return
    end if

    ' Remux / transcode: kick the per-session ffmpeg job. Video
    ' node sits idle until the task lands and we wire its content.
    m.transcodeTask.itemId = m.top.itemId
    m.transcodeTask.fileId = m.file.id
    m.transcodeTask.height = decision.height
    m.transcodeTask.videoCopy = decision.videoCopy
    m.transcodeTask.supportsHevc = decision.supportsHevc
    m.transcodeTask.audioStreamIndex = -1
    m.transcodeTask.positionMs = 0
    m.transcodeTask.control = "RUN"
    ' Cache the inputs so the audio picker can re-issue at a new
    ' audio_stream_index without re-running Playback_Decide.
    m.lastTranscodeRequest = {
        itemId: m.top.itemId,
        fileId: m.file.id,
        height: decision.height,
        videoCopy: decision.videoCopy,
        supportsHevc: decision.supportsHevc
    }
end sub

sub startDirectPlayback(serverUrl as String)
    streamToken = m.file.stream_token
    if streamToken = invalid or streamToken = ""
        streamToken = Prefs_GetAccessToken()
    end if
    if streamToken = invalid or streamToken = ""
        bailToHome()
        return
    end if

    content = createObject("roSGNode", "ContentNode")
    content.title = m.item.title
    content.url = AssetStream(serverUrl, m.file.id, streamToken)
    content.streamFormat = guessStreamFormat(m.file.path)
    if m.item.view_offset_ms <> invalid and m.item.view_offset_ms > 0
        content.playStart = Int(m.item.view_offset_ms / 1000)
    end if

    m.video.content = content
    m.video.control = "play"
    m.video.setFocus(true)
end sub

sub onTranscodeTaskState()
    if m.transcodeTask.state <> "done" then return
    sess = m.transcodeTask.result
    if sess = invalid or sess.session_id = invalid
        ' Session start failed — bail rather than silently fall
        ' back to direct play, which would error in the same way.
        bailToHome()
        return
    end if

    serverUrl = Prefs_GetServerUrl()
    if serverUrl = invalid
        bailToHome()
        return
    end if

    m.session = { session_id: sess.session_id, token: sess.token }

    ' Server returns playlist_url already-relative + already-tokenised
    ' (?token=<seg-token>). Just prepend the origin to make absolute.
    playlistUrl = serverUrl + sess.playlist_url

    content = createObject("roSGNode", "ContentNode")
    content.title = m.item.title
    content.url = playlistUrl
    content.streamFormat = "hls"
    if m.item.view_offset_ms <> invalid and m.item.view_offset_ms > 0
        content.playStart = Int(m.item.view_offset_ms / 1000)
    end if

    m.video.content = content
    m.video.control = "play"
    m.video.setFocus(true)
end sub

sub onVideoState()
    state = m.video.state
    if state = "finished"
        ' Music tracks + audiobook chapters chain silently when a
        ' next sibling exists — the closing seconds carry the
        ' outro / final line and an Up Next overlay would clip
        ' that. Episodes / podcasts go through goToNext via the
        ' Up Next overlay flow (which calls in here when its
        ' countdown elapses or the user accepts).
        if m.nextSibling <> invalid and (m.item <> invalid) and (m.item.type = "track" or m.item.type = "audiobook_chapter" or m.item.type = "podcast_episode" or m.item.type = "episode")
            goToNext(m.nextSibling)
            return
        end if
        stopTranscodeSession()
        bailToHome()
    else if state = "error"
        stopTranscodeSession()
        bailToHome()
    end if
end sub

' Position tick handler. Roku's Video node updates `position`
' (in seconds, float) several times per second during playback;
' we use it as our universal scheduler for marker windows and the
' Up Next overlay lead-in.
sub onVideoPosition()
    posMs = Int(m.video.position * 1000)
    updateActiveMarker(posMs)
    maybeShowUpNext(posMs)
end sub

' ── Markers ────────────────────────────────────────────────────────

sub onMarkersTaskState()
    if m.markersTask.state <> "done" then return
    list = m.markersTask.result
    if list = invalid then list = []
    m.markers = list
end sub

sub updateActiveMarker(posMs as Integer)
    if m.markers = invalid or m.markers.Count() = 0
        if m.skipMarker.visible then m.skipMarker.visible = false
        m.activeMarker = invalid
        return
    end if
    found = invalid
    for each m_ in m.markers
        if posMs >= m_.start_ms and posMs < m_.end_ms
            key = m_.start_ms.ToStr()
            if not m.dismissedMarkers.DoesExist(key)
                found = m_
                exit for
            end if
        end if
    end for
    if found = invalid
        if m.skipMarker.visible then m.skipMarker.visible = false
        m.activeMarker = invalid
        return
    end if
    if m.activeMarker = invalid or m.activeMarker.start_ms <> found.start_ms
        m.activeMarker = found
        if found.kind = "credits"
            m.skipMarker.text = "* Skip Credits"
        else
            m.skipMarker.text = "* Skip Intro"
        end if
        m.skipMarker.visible = true
    end if
end sub

' Skip-marker invocation. Called from onKeyEvent when the user
' presses * with an active marker.
sub skipActiveMarker()
    if m.activeMarker = invalid then return
    key = m.activeMarker.start_ms.ToStr()
    m.dismissedMarkers[key] = true
    m.video.seek = m.activeMarker.end_ms / 1000
    m.skipMarker.visible = false
    m.activeMarker = invalid
end sub

' ── Up Next ────────────────────────────────────────────────────────

sub onChildrenTaskState()
    if m.childrenTask.state <> "done" then return
    kids = m.childrenTask.result
    if kids = invalid or m.item = invalid then return
    if m.item.index = invalid then return
    targetIdx = m.item.index + 1
    for each k in kids
        if k.type = m.item.type and k.index <> invalid and k.index = targetIdx
            m.nextSibling = k
            return
        end if
    end for
end sub

sub maybeShowUpNext(posMs as Integer)
    if m.upNextShown then return
    if m.nextSibling = invalid or m.item = invalid then return
    ' Music tracks + audiobook chapters chain silently at EOS — no
    ' overlay because the closing seconds are part of the content.
    if m.item.type = "track" or m.item.type = "audiobook_chapter" then return
    durationMs = Int(m.video.duration * 1000)
    if durationMs <= 0 then return
    if durationMs - posMs > m.UP_NEXT_LEAD_MS then return

    m.upNextShown = true
    m.upNextTitle.text = m.nextSibling.title
    m.upNext.visible = true
end sub

' Skip / accept Up Next from a key press. Wired in onKeyEvent.
sub acceptUpNext()
    if m.nextSibling <> invalid then goToNext(m.nextSibling)
end sub

sub dismissUpNext()
    m.upNext.visible = false
    m.nextSibling = invalid
end sub

sub goToNext(target as Object)
    stopTranscodeSession()
    getMainScene().callFunc("navigateToWithItem", "PlayerScene", target.id)
end sub

' ── Cross-device sync ──────────────────────────────────────────────

' Polls /items/{id} every 5 s while the player is paused. Snaps
' the playhead to view_offset_ms when it diverges from local —
' the convention is "the device that's actively playing wins, the
' paused player accepts updates from elsewhere." Self-loop guard
' ignores echoes within 2 s of our own most recent progress write.
sub onSyncTimerFire()
    if m.video = invalid then return
    if m.video.state <> "paused" then return
    if m.top.itemId = invalid or m.top.itemId = "" then return
    parsed = Client_GetSync(ApiItem(m.top.itemId), true)
    if parsed = invalid then return
    if parsed.view_offset_ms = invalid then return
    newPos = parsed.view_offset_ms
    if m.lastReportedPositionMs >= 0 and Abs(newPos - m.lastReportedPositionMs) < 2000 then return
    localMs = Int(m.video.position * 1000)
    if Abs(newPos - localMs) < 2000 then return
    m.video.seek = newPos / 1000
end sub

' Routed by the channel's onKeyEvent in the application. Roku
' delivers key strings: "OK", "back", "options" (the * button),
' "right" / "left" / etc. We handle the overlay-specific keys here
' so the user can interact without focus tricks (the Video node
' captures focus during playback).
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    ' Track picker takes priority when open — back closes it (without
    ' leaving the player), * cycles to the next mode (audio →
    ' subtitle → closed), up/down/OK ride through to the LabelList.
    if m.trackPickerMode <> ""
        if key = "back"
            closeTrackPicker()
            return true
        end if
        if key = "options" or key = "*"
            cycleTrackPicker()
            return true
        end if
        ' Let LabelList swallow up/down/OK natively.
        return false
    end if
    ' Skip-marker overlay: * (options key) skips, back dismisses
    ' that marker for the rest of this play.
    if m.activeMarker <> invalid
        if key = "options" or key = "*"
            skipActiveMarker()
            return true
        end if
    end if
    ' Up Next overlay: OK accepts immediately, back dismisses.
    if m.upNextShown
        if key = "OK"
            acceptUpNext()
            return true
        end if
        if key = "back"
            dismissUpNext()
            return true
        end if
    end if
    ' * (options) with no overlay open → opens the audio picker
    ' (or subtitle picker if there's no audio choice but subtitles
    ' exist). Same key cycles through the modes once open.
    if key = "options" or key = "*"
        if hasMultiAudio()
            openTrackPicker("audio")
            return true
        end if
        if hasSubtitles()
            openTrackPicker("subtitle")
            return true
        end if
    end if
    return false
end function

' ── Audio + subtitle picker ────────────────────────────────────────

function hasMultiAudio() as Boolean
    if m.file = invalid or m.file.audio_streams = invalid then return false
    return m.file.audio_streams.Count() > 1
end function

function hasSubtitles() as Boolean
    if m.file = invalid or m.file.subtitle_streams = invalid then return false
    return m.file.subtitle_streams.Count() > 0
end function

sub openTrackPicker(mode as String)
    m.trackPickerMode = mode
    if mode = "audio"
        m.trackPickerTitle.text = "AUDIO  ·  * for subtitles  ·  back to close"
        m.trackPickerList.content = buildAudioContent()
        m.trackPickerList.jumpToItem = m.activeAudioIndex
    else
        m.trackPickerTitle.text = "SUBTITLES  ·  * to close  ·  back to close"
        m.trackPickerList.content = buildSubtitleContent()
        ' +1 because index 0 in the LabelList is the synthetic "Off" row.
        cursor = 0
        if m.activeSubtitleIndex >= 0 then cursor = m.activeSubtitleIndex + 1
        m.trackPickerList.jumpToItem = cursor
    end if
    m.trackPicker.visible = true
    m.trackPickerList.setFocus(true)
end sub

sub cycleTrackPicker()
    if m.trackPickerMode = "audio"
        if hasSubtitles()
            openTrackPicker("subtitle")
        else
            closeTrackPicker()
        end if
    else
        ' From subtitle: close. The opening cycle is audio → subtitle
        ' → closed; subtitle-only flows just open and close.
        closeTrackPicker()
    end if
end sub

sub closeTrackPicker()
    m.trackPickerMode = ""
    m.trackPicker.visible = false
    m.video.setFocus(true)
end sub

function buildAudioContent() as Object
    root = createObject("roSGNode", "ContentNode")
    if m.file = invalid or m.file.audio_streams = invalid then return root
    for i = 0 to m.file.audio_streams.Count() - 1
        s = m.file.audio_streams[i]
        node = root.createChild("ContentNode")
        prefix = "  "
        if i = m.activeAudioIndex then prefix = "● "
        lang = "und"
        if s.language <> invalid and s.language <> "" then lang = s.language
        codec = ""
        if s.codec <> invalid then codec = s.codec
        ch = ""
        if s.channels <> invalid then ch = s.channels.ToStr() + "ch"
        parts = [lang]
        if codec <> "" then parts.push(codec)
        if ch <> "" then parts.push(ch)
        if s.title <> invalid and s.title <> "" then parts.push(s.title)
        node.title = prefix + buildJoin(parts, " · ")
    end for
    return root
end function

function buildSubtitleContent() as Object
    root = createObject("roSGNode", "ContentNode")
    offNode = root.createChild("ContentNode")
    if m.activeSubtitleIndex < 0
        offNode.title = "● Off"
    else
        offNode.title = "  Off"
    end if
    if m.file = invalid or m.file.subtitle_streams = invalid then return root
    for i = 0 to m.file.subtitle_streams.Count() - 1
        s = m.file.subtitle_streams[i]
        node = root.createChild("ContentNode")
        prefix = "  "
        if i = m.activeSubtitleIndex then prefix = "● "
        lang = "und"
        if s.language <> invalid and s.language <> "" then lang = s.language
        parts = [lang]
        if s.forced = true then parts.push("forced")
        if s.title <> invalid and s.title <> "" then parts.push(s.title)
        node.title = prefix + buildJoin(parts, " · ")
    end for
    return root
end function

function buildJoin(parts as Object, sep as String) as String
    out = ""
    first = true
    for each p in parts
        if first
            out = p
            first = false
        else
            out = out + sep + p
        end if
    end for
    return out
end function

' LabelList delivers itemSelected when the user presses OK on a row.
sub onTrackPickerSelect()
    idx = m.trackPickerList.itemSelected
    if m.trackPickerMode = "audio"
        if m.file <> invalid and m.file.audio_streams <> invalid
            picked = m.file.audio_streams[idx]
            if picked <> invalid and idx <> m.activeAudioIndex
                m.activeAudioIndex = idx
                ' Server-side audio_stream_index is the stream's
                ' "index" field (the ffprobe stream index), not its
                ' position in the array. Pass that through.
                streamIdx = picked.index
                if streamIdx = invalid then streamIdx = idx
                reissueAudioTrack(streamIdx)
            end if
        end if
        closeTrackPicker()
    else if m.trackPickerMode = "subtitle"
        if idx = 0
            m.activeSubtitleIndex = -1
            ' Roku's Video node uses globalCaptionMode + the
            ' selected subtitle index from `availableSubtitleTracks`.
            ' Setting subtitleTrack to "" disables.
            m.video.subtitleTrack = ""
        else
            m.activeSubtitleIndex = idx - 1
            applySubtitleSelection(m.activeSubtitleIndex)
        end if
        closeTrackPicker()
    end if
end sub

' Re-issue the active transcode session at the current playhead with
' a new audio_stream_index. Server emits one audio per session for
' transcode/remux mode, so language switching can't ride the player's
' track selector — only a fresh session carries the chosen language.
sub reissueAudioTrack(audioStreamIndex as Integer)
    if m.lastTranscodeRequest = invalid then return
    posMs = Int(m.video.position * 1000)
    stopTranscodeSession()
    req = m.lastTranscodeRequest
    m.transcodeTask.itemId = req.itemId
    m.transcodeTask.fileId = req.fileId
    m.transcodeTask.height = req.height
    m.transcodeTask.videoCopy = req.videoCopy
    m.transcodeTask.supportsHevc = req.supportsHevc
    m.transcodeTask.audioStreamIndex = audioStreamIndex
    m.transcodeTask.positionMs = posMs
    m.transcodeTask.control = "RUN"
end sub

' Map the user's pick to the firmware Video node's subtitle selector.
' availableSubtitleTracks is populated by Roku once playback starts;
' setting subtitleTrack to one of its TrackName entries enables it.
sub applySubtitleSelection(streamIdx as Integer)
    avail = m.video.availableSubtitleTracks
    if avail = invalid or avail.Count() = 0
        ' Subtitles aren't surfaced yet (race during initial playlist
        ' parse) — store the pick and the LabelList will retry on
        ' the next user interaction. Best-effort.
        return
    end if
    ' availableSubtitleTracks order matches the file's subtitle_streams
    ' order. Roku's TrackName field is what subtitleTrack expects.
    if streamIdx >= 0 and streamIdx < avail.Count()
        m.video.subtitleTrack = avail[streamIdx].TrackName
    end if
end sub

' Tear down the active transcode session via DELETE. Best-effort —
' we don't block on the response, and a server idle-timeout sweep
' eventually cleans up any leaked sessions anyway. The point is to
' free the GPU / CPU slot promptly when the user navigates off.
sub stopTranscodeSession()
    if m.session = invalid then return
    transfer = Client_BuildTransfer(ApiTranscodeStop(m.session.session_id, m.session.token), false)
    if transfer <> invalid
        transfer.SetRequest("DELETE")
        transfer.AsyncGetToString()
    end if
    m.session = invalid
end sub

' Best-effort stream-format guess from file extension. The Go
' server returns the original filename in `path`; Roku's Video
' node uses streamFormat to pick mp4 / hls / dash demuxers.
function guessStreamFormat(path as String) as String
    if path = invalid then return "mp4"
    lower = LCase(path)
    if Right(lower, 5) = ".m3u8" then return "hls"
    if Right(lower, 4) = ".mpd" then return "dash"
    if Right(lower, 4) = ".mkv" then return "mkv"
    if Right(lower, 4) = ".mov" then return "mp4"
    if Right(lower, 4) = ".m4v" then return "mp4"
    return "mp4"
end function

sub bailToHome()
    stopTranscodeSession()
    getMainScene().callFunc("navigateTo", "HomeScene")
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
