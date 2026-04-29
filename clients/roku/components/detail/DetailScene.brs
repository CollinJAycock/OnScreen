' DetailScene controller. Caller sets `itemId` on m.top before
' mount; init fires the item + children fetch tasks in parallel,
' then renders a hero + Play button + children carousel.

sub init()
    m.fanart = m.top.findNode("fanart")
    m.title = m.top.findNode("title")
    m.meta = m.top.findNode("meta")
    m.summary = m.top.findNode("summary")
    m.playBtn = m.top.findNode("playBtn")
    m.childrenHeader = m.top.findNode("childrenHeader")
    m.childrenList = m.top.findNode("children")
    m.loading = m.top.findNode("loading")

    m.itemTask = m.top.findNode("itemTask")
    m.childrenTask = m.top.findNode("childrenTask")

    m.item = invalid
    m.children = []

    m.itemTask.observeField("state", "onItemTaskState")
    m.childrenTask.observeField("state", "onChildrenTaskState")
    m.playBtn.observeField("buttonSelected", "onPlayPressed")
    m.childrenList.observeField("rowItemSelected", "onChildSelected")
    m.top.observeField("itemId", "onItemIdSet")

    if m.top.itemId <> invalid and m.top.itemId <> ""
        kickoff()
    end if
end sub

sub onItemIdSet()
    if m.top.itemId <> invalid and m.top.itemId <> "" and m.item = invalid
        kickoff()
    end if
end sub

sub kickoff()
    m.itemTask.itemId = m.top.itemId
    m.itemTask.control = "RUN"
    m.childrenTask.itemId = m.top.itemId
    m.childrenTask.control = "RUN"
end sub

sub onItemTaskState()
    if m.itemTask.state <> "done" then return
    item = m.itemTask.result
    if item = invalid or item.id = invalid
        m.loading.text = "Couldn't load item details"
        return
    end if
    m.item = item
    renderItem()
end sub

sub onChildrenTaskState()
    if m.childrenTask.state <> "done" then return
    list = m.childrenTask.result
    if list = invalid then list = []
    m.children = list
    renderChildren()
end sub

sub renderItem()
    item = m.item
    if item = invalid then return

    m.loading.visible = false

    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()

    if item.fanart_path <> invalid and serverUrl <> invalid and token <> invalid
        m.fanart.uri = AssetArtwork(serverUrl, item.fanart_path, 1920, token)
    else if item.poster_path <> invalid and serverUrl <> invalid and token <> invalid
        ' Fall back to the poster scaled to 1920 — better than a
        ' bare dark rectangle for items without dedicated fanart.
        m.fanart.uri = AssetArtwork(serverUrl, item.poster_path, 1920, token)
    end if

    m.title.text = item.title
    m.meta.text = buildMetaLine(item)
    if item.summary <> invalid then m.summary.text = item.summary

    ' Play button label depends on whether we're resuming. Movies
    ' and leaf items get "Resume Nm"; containers (show / album /
    ' podcast / multi-file audiobook) just say "Play" and pick
    ' the first child to play on press.
    label = "Play"
    if item.view_offset_ms <> invalid and item.view_offset_ms > 0 and isLeafType(item.type)
        mins = Int(item.view_offset_ms / 60000)
        label = "Resume " + mins.ToStr() + "m"
    end if
    m.playBtn.text = label
    m.playBtn.setFocus(true)
end sub

sub renderChildren()
    if m.children.Count() = 0
        m.childrenHeader.visible = false
        m.childrenList.visible = false
        return
    end if

    ' Header label depends on the parent type — same wording the
    ' Android detail page uses so users see the same mental model
    ' across surfaces.
    parentType = ""
    if m.item <> invalid then parentType = m.item.type
    if parentType = "album"
        m.childrenHeader.text = "Tracks"
    else if parentType = "audiobook"
        m.childrenHeader.text = "Chapters"
    else if parentType = "artist"
        m.childrenHeader.text = "Albums"
    else
        m.childrenHeader.text = "Episodes"
    end if
    m.childrenHeader.visible = true

    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()

    root = createObject("roSGNode", "ContentNode")
    row = root.createChild("ContentNode")
    for each child in m.children
        node = row.createChild("ContentNode")
        ' Episodes + chapters: prefix the index for at-a-glance
        ' ordering ("1. Pilot"). Tracks and other types use the
        ' plain title — index numbers on a Pink Floyd album are
        ' just visual noise next to the track name.
        if child.index <> invalid and (child.type = "episode" or child.type = "audiobook_chapter")
            node.title = child.index.ToStr() + ". " + child.title
        else
            node.title = child.title
        end if
        node.id = child.id
        node.addField("itemType", "string", false)
        node.itemType = child.type
        artPath = invalid
        if child.thumb_path <> invalid then artPath = child.thumb_path
        if artPath = invalid and child.poster_path <> invalid then artPath = child.poster_path
        if artPath <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 400, token)
        end if
    end for
    m.childrenList.content = root
    m.childrenList.visible = true
end sub

' Children of the carousel were OK-pressed — fire the player. We
' don't drill into a sub-detail for these; episodes / tracks /
' chapters are leaves the user explicitly chose. Same model as the
' Android EpisodeAdapter callback.
sub onChildSelected()
    selected = m.childrenList.rowItemSelected
    if selected = invalid then return
    rowIdx = selected[0]
    itemIdx = selected[1]
    rowNode = m.childrenList.content.getChild(rowIdx)
    if rowNode = invalid then return
    childNode = rowNode.getChild(itemIdx)
    if childNode = invalid then return
    getMainScene().callFunc("navigateToWithItem", "PlayerScene", childNode.id)
end sub

' Play pressed on the parent. Leaf items play themselves; container
' types pick the first child as the play target — no in-progress /
' first-unwatched logic yet (same simplification the webOS port
' shipped with).
sub onPlayPressed()
    if m.item = invalid then return
    target = m.item.id
    if not isLeafType(m.item.type)
        if m.children.Count() = 0
            return
        end if
        target = m.children[0].id
    end if
    getMainScene().callFunc("navigateToWithItem", "PlayerScene", target)
end sub

function isLeafType(t as String) as Boolean
    return t = "movie" or t = "episode" or t = "track" or t = "audiobook_chapter" or t = "podcast_episode"
end function

function buildMetaLine(item as Object) as String
    parts = []
    if item.year <> invalid then parts.push(item.year.ToStr())
    if item.content_rating <> invalid and item.content_rating <> "" then parts.push(item.content_rating)
    if item.duration_ms <> invalid and item.duration_ms > 0
        mins = Int(item.duration_ms / 60000)
        if mins >= 60
            h = Int(mins / 60)
            mm = mins mod 60
            parts.push(h.ToStr() + "h " + mm.ToStr() + "m")
        else
            parts.push(mins.ToStr() + "m")
        end if
    end if
    if item.rating <> invalid and item.rating > 0
        parts.push("★ " + Str(item.rating))
    end if
    if item.genres <> invalid and item.genres.Count() > 0
        parts.push(item.genres[0])
    end if
    out = ""
    for i = 0 to parts.Count() - 1
        if i > 0 then out = out + "  ·  "
        out = out + parts[i]
    end for
    return out
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
