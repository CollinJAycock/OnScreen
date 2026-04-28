' PlayerScene controller. itemId is set by the caller (HomeScene)
' before mount; on init we fire the ItemFetchTask, then feed the
' resolved stream URL into the Video node.

sub init()
    m.video = m.top.findNode("video")
    m.itemTask = m.top.findNode("itemTask")

    m.itemTask.observeField("state", "onItemTaskState")
    m.video.observeField("state", "onVideoState")

    if m.top.itemId = invalid or m.top.itemId = ""
        ' Defensive: nothing to play. Pop back.
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

    file = item.files[0]
    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()
    if serverUrl = invalid or token = invalid
        bailToHome()
        return
    end if

    content = createObject("roSGNode", "ContentNode")
    content.title = item.title
    content.url = AssetStream(serverUrl, file.id, token)
    ' Stream format hint so the Video node picks the right
    ' demuxer. The Go server's /media/stream/{id} serves the file
    ' direct (any format ffprobe identified at scan time); for
    ' transcoded HLS sessions the URL would point at
    ' /transcode/.../playlist.m3u8 and streamFormat would be
    ' "hls" — wire up when the transcode negotiation lands.
    content.streamFormat = guessStreamFormat(file.path)
    if item.view_offset_ms <> invalid and item.view_offset_ms > 0
        content.playStart = Int(item.view_offset_ms / 1000)
    end if

    m.video.content = content
    m.video.control = "play"
    m.video.setFocus(true)
end sub

sub onVideoState()
    state = m.video.state
    if state = "finished" or state = "error"
        bailToHome()
    end if
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
    getMainScene().callFunc("navigateTo", "HomeScene")
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
