' MainScene controller. Decides on launch which sub-scene to show
' (Setup → Login → Home) and exposes a navigateTo() helper sub-
' scenes call when they finish their step.
'
' The scene graph itself is declared in MainScene.xml; this file
' wires behaviour. Keep view structure in the XML, behaviour here.
'
' Navigation back-stack (Roku certification requires Back to return
' to the previous screen, only exiting the channel at the root home
' screen): every navigateTo/navigateToWithItem pushes a frame
' { name, itemId } onto m.navStack and mounts the scene. onKeyEvent
' handles key="back" by popping to the parent frame; only the root
' frame allows the Back press to bubble out (which exits the
' channel). Child scenes leave "back" unhandled (return false) so
' the event reaches MainScene's onKeyEvent and the stack drives the
' transition.
'
' Roku certification also needs:
' - Launch beacons (reportReady): AppLaunchComplete once the first screen
'   the user can use is up (Home's rows, a deep-linked title page, or its
'   playback). A sign-in or server screen at launch is a dialog, bracketed
'   by AppDialogInitiate / AppDialogComplete, and doesn't count.
' - Deep links (onDeepLink): open the linked title, playing it for movies
'   and episodes, with Home under it so Back leads there.

sub init()
    m.content = m.top.findNode("content")
    m.detached = m.top.findNode("detached")
    m.currentChild = invalid
    ' Back-stack of { name, itemId } frames. The bottom frame is the
    ' root (channel-exit) screen; everything above it pops on Back.
    m.navStack = []
    ' Launch beacons, each sent once (reportReady, navigateToWithParams).
    m.launchReported = false
    m.launchDialog = false
    ' A parsed deep link waiting for start() or for sign-in to finish.
    m.pendingDeepLink = invalid
    m.started = false
end sub

' The first routing, called by main.brs after it has set any launch deep
' link (init runs inside CreateScene, before that).
sub start()
    if m.started then return
    m.started = true
    if not Prefs_HasServer()
        navigateTo("ServerSetupScene")
    else if not Prefs_IsLoggedIn()
        navigateTo("LoginScene")
    else
        navigateTo("HomeScene")
    end if
end sub

' The deepLink field changed: a launch link (before start) or an
' roInputEvent while running. A link that isn't an item is dropped. Before
' start, or while signed out, it waits: navigateToWithParams opens it in
' place of Home once there is a session.
sub onDeepLink()
    dl = DeepLink_Parse(m.top.deepLink)
    if dl = invalid then return
    if not m.started or not Prefs_HasServer() or not Prefs_IsLoggedIn()
        m.pendingDeepLink = dl
        return
    end if
    openDeepLink(dl)
end sub

' Mount the linked title over a stack of Home → title page (→ player), so
' Back from playback reaches the title's page and then Home, which exits.
' Only the top frame is mounted; the others mount when Back reaches them.
sub openDeepLink(dl as Object)
    m.navStack = [{ name: "HomeScene", itemId: "", params: {} }, { name: "DetailScene", itemId: dl.contentId, params: {} }]
    if dl.play then m.navStack.push({ name: "PlayerScene", itemId: dl.contentId, params: {} })
    top = m.navStack.peek()
    mountScene(top.name, top.itemId, top.params)
end sub

' A scene the user can use is on screen: Home's rows (or its error),
' a title page, or playback. Sends AppLaunchComplete the first time.
sub reportReady()
    if m.launchReported then return
    m.launchReported = true
    m.top.signalBeacon("AppLaunchComplete")
end sub

' Replace the active child with a fresh instance of `name`. Sub-
' scenes call this on their controller via getScene().callFunc()
' rather than navigating themselves — keeps routing in one place.
sub navigateTo(name as String)
    navigateToWithItem(name, "")
end sub

' navigateToWithItem mounts a fresh `name` and sets `itemId` on it
' before attaching. The scene's init() has already run by then (it runs
' inside createObject), so a scene observes itemId rather than reading it
' in init(). Item-aware sub-scenes (DetailScene, PlayerScene)
' need the id available before mount; the previous pattern of "create
' a node here, set itemId, then call navigateTo (which then creates
' ANOTHER node and discards the first)" was both a leak AND broken
' — the second node had no itemId so init() bailed straight to
' HomeScene. Setting the field on the surviving instance fixes both.
'
' This is the public (forward) navigation entry point: it records a
' new frame on the back-stack. The "root" scenes (HomeScene plus the
' setup-flow entry points) reset the stack to a single frame — they
' are launch / bail targets, not destinations you Back into, so the
' user exits the channel from there.
sub navigateToWithItem(name as String, itemId as String)
    navigateToWithParams(name, itemId, {})
end sub

' navigateToWithItem plus more interface fields for the new scene, set
' before mount like itemId: params is { field: value } (e.g. PlayerScene's
' fileId for a chosen version). Fields the scene doesn't declare are
' skipped. Kept on the frame, so Back re-mounts the scene the same way.
sub navigateToWithParams(name as String, itemId as String, params as Object)
    ' Setup or sign-in before the channel has launched is a launch dialog.
    if not m.launchReported
        if name = "ServerSetupScene" or name = "LoginScene"
            if not m.launchDialog then m.top.signalBeacon("AppDialogInitiate")
            m.launchDialog = true
        else if m.launchDialog
            m.top.signalBeacon("AppDialogComplete")
            m.launchDialog = false
        end if
    end if
    ' Home with a deep link waiting (launch, or one that came in signed
    ' out): the linked title instead, with Home under it.
    if name = "HomeScene" and m.pendingDeepLink <> invalid
        dl = m.pendingDeepLink
        m.pendingDeepLink = invalid
        openDeepLink(dl)
        return
    end if
    if isRootScene(name)
        m.navStack = []
    else if m.navStack.Count() > 0 and m.currentChild <> invalid and m.currentChild.hasField("focusState")
        ' The scene being left records where the viewer was (its focused
        ' card, a search query); Back hands it back as restoreState.
        m.navStack.peek().saved = m.currentChild.focusState
    end if
    m.navStack.push({ name: name, itemId: itemId, params: params })
    mountScene(name, itemId, params)
end sub

' True for the scenes that form the bottom of the back-stack. Pressing
' Back on any of these exits the channel (the certification-required
' behaviour for the home screen) rather than popping further.
function isRootScene(name as String) as Boolean
    return name = "HomeScene" or name = "ServerSetupScene" or name = "LoginScene"
end function

' Swap the active child for a fresh instance of `name` with `itemId`
' (and any params fields) pre-set. Does NOT touch the back-stack — both
' forward navigation and Back-pop funnel through here for the actual
' mount.
sub mountScene(name as String, itemId as String, params as Dynamic)
    if m.currentChild <> invalid
        m.content.removeChild(m.currentChild)
    end if
    child = createObject("roSGNode", name)
    if child = invalid
        ' Bad name (typo, missing component) — log and bail; better
        ' than mounting an invalid pointer.
        print "MainScene: unknown child scene "; name
        return
    end if
    if params <> invalid and type(params) = "roAssociativeArray"
        for each field in params
            if child.hasField(field) then child.setField(field, params[field])
        end for
    end if
    if itemId <> "" and child.hasField("itemId")
        child.itemId = itemId
    end if
    m.currentChild = child
    m.content.appendChild(child)
    ' A scene names the node it wants focused (initialFocus): its own
    ' setFocus in init() ran before it was attached, and focusing the scene
    ' Group itself would leave its keyboard or field without the keys.
    target = invalid
    if child.hasField("initialFocus") then target = child.initialFocus
    if target <> invalid
        target.setFocus(true)
    else
        child.setFocus(true)
    end if
end sub

' Fire-and-forget API call that must outlive the scene asking for it —
' the player's final progress report and its transcode-session DELETE go
' out right before the player unmounts. req is { method, path, body?,
' auth? }. The ApiCallTask is parented here (MainScene lives as long as
' the channel) and dropped once it finishes; nobody reads the result.
sub runDetachedCall(req as Object)
    if req = invalid or req.path = invalid or req.path = "" then return
    t = createObject("roSGNode", "ApiCallTask")
    if req.method <> invalid then t.method = req.method
    t.path = req.path
    if req.body <> invalid then t.body = req.body
    if req.auth <> invalid then t.auth = req.auth
    t.observeField("state", "onDetachedCallState")
    m.detached.appendChild(t)
    t.control = "RUN"
end sub

sub onDetachedCallState(evt as Object)
    t = evt.getRoSGNode()
    if t.state = "done" or t.state = "stop" then m.detached.removeChild(t)
end sub

' Back handling for the whole channel. Child scenes (Group-based) sit
' under this Scene in the focus chain; when they leave "back"
' unhandled the event bubbles here. If there's a parent frame on the
' stack, pop to it and consume the press (return true). At the root
' frame we return false so the firmware exits the channel — the only
' place a Back press is allowed to leave OnScreen.
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key <> "back" then return false
    if m.navStack.Count() <= 1 then return false

    ' Drop the current frame and re-mount the one beneath it.
    m.navStack.pop()
    parent = m.navStack.peek()
    params = {}
    if parent.params <> invalid then params.Append(parent.params)
    if parent.saved <> invalid then params.restoreState = parent.saved
    mountScene(parent.name, parent.itemId, params)
    return true
end function
