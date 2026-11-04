' LibraryListScene controller. GET /api/v1/libraries → list →
' click → navigateToWithItem("LibraryScene", library_id).
'
' We keep the resolved library list as `m.libraries` so the
' click handler can map list-index → library_id without a second
' fetch. LabelList only carries text + index out of the box.

sub init()
    m.list = m.top.findNode("list")
    m.empty = m.top.findNode("empty")
    m.task = m.top.findNode("task")
    m.libraries = []

    m.task.observeField("state", "onTaskState")
    m.list.observeField("itemSelected", "onLibrarySelected")

    m.task.path = ApiLibraries()
    m.task.control = "RUN"
end sub

sub onTaskState()
    if m.task.state <> "done" then return
    libs = m.task.result
    if libs = invalid or libs.Count() = 0
        m.empty.visible = true
        return
    end if

    ' Filter out soft-deleted libraries client-side. The server
    ' usually does this in the handler, but the field comes back
    ' on the row anyway — being defensive lets the client survive
    ' an older server build that doesn't filter.
    visible = []
    for each lib in libs
        if lib.deleted_at = invalid then visible.push(lib)
    end for
    if visible.Count() = 0
        m.empty.visible = true
        return
    end if

    m.libraries = visible

    ' One row per library: its name, then its kind in words ("Ellie Videos ·
    ' Home videos") so like-named libraries stay apart. The kind is left
    ' off when the name already says it ("Movies", "Anime").
    root = createObject("roSGNode", "ContentNode")
    for each lib in visible
        node = root.createChild("ContentNode")
        label = lib.name
        kind = libraryKindLabel(lib.type)
        if kind <> "" and Instr(1, LCase(label), LCase(kind)) = 0 then label = label + "  ·  " + kind
        node.title = label
    end for
    m.list.content = root
    m.list.setFocus(true)
end sub

sub onLibrarySelected()
    idx = m.list.itemSelected
    if idx < 0 or idx >= m.libraries.Count() then return
    libraryId = m.libraries[idx].id
    if libraryId = invalid or libraryId = "" then return
    name = m.libraries[idx].name
    if type(name) <> "String" and type(name) <> "roString" then name = ""
    libType = m.libraries[idx].type
    if type(libType) <> "String" and type(libType) <> "roString" then libType = ""
    getMainScene().callFunc("navigateToWithParams", "LibraryScene", libraryId, { heading: name, libraryType: libType })
end sub

' A library type as a person would say it; "" for an unknown one.
function libraryKindLabel(t as Dynamic) as String
    if type(t) <> "String" and type(t) <> "roString" then return ""
    kinds = { movie: "Movies", show: "TV shows", tv: "TV shows", anime: "Anime", music: "Music", photo: "Photos", audiobook: "Audiobooks", book: "Books", podcast: "Podcasts", home_video: "Home videos", cartoons: "Cartoons", livetv: "Live TV" }
    k = kinds[LCase(t)]
    if k = invalid then return ""
    return k
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
