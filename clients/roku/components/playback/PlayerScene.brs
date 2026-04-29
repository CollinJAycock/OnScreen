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

    m.session = invalid ' { session_id, token } when remux/transcode
    m.item = invalid
    m.file = invalid

    m.itemTask.observeField("state", "onItemTaskState")
    m.transcodeTask.observeField("state", "onTranscodeTaskState")
    m.video.observeField("state", "onVideoState")

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
    m.transcodeTask.control = "RUN"
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
    if state = "finished" or state = "error"
        stopTranscodeSession()
        bailToHome()
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
