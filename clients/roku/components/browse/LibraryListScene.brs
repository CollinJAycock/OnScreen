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

    ' Build the label content. "Movies (movie)" — type-tag in
    ' parentheses so a user with two like-named libraries (e.g.
    ' "Movies" + "4K Movies") can still tell them apart.
    root = createObject("roSGNode", "ContentNode")
    for each lib in visible
        node = root.createChild("ContentNode")
        label = lib.name
        if lib.type <> invalid and lib.type <> ""
            label = label + "    (" + lib.type + ")"
        end if
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
    getMainScene().callFunc("navigateToWithItem", "LibraryScene", libraryId)
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
