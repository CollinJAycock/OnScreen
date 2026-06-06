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

sub init()
    m.content = m.top.findNode("content")
    m.currentChild = invalid
    ' Back-stack of { name, itemId } frames. The bottom frame is the
    ' root (channel-exit) screen; everything above it pops on Back.
    m.navStack = []

    if not Prefs_HasServer()
        navigateTo("ServerSetupScene")
    else if not Prefs_IsLoggedIn()
        navigateTo("LoginScene")
    else
        navigateTo("HomeScene")
    end if
end sub

' Replace the active child with a fresh instance of `name`. Sub-
' scenes call this on their controller via getScene().callFunc()
' rather than navigating themselves — keeps routing in one place.
sub navigateTo(name as String)
    navigateToWithItem(name, "")
end sub

' navigateToWithItem mounts a fresh `name` and sets `itemId` on it
' before attaching, so the new scene's init() observes the field as
' already populated. Item-aware sub-scenes (DetailScene, PlayerScene)
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
    if isRootScene(name)
        m.navStack = []
    end if
    m.navStack.push({ name: name, itemId: itemId })
    mountScene(name, itemId)
end sub

' True for the scenes that form the bottom of the back-stack. Pressing
' Back on any of these exits the channel (the certification-required
' behaviour for the home screen) rather than popping further.
function isRootScene(name as String) as Boolean
    return name = "HomeScene" or name = "ServerSetupScene" or name = "LoginScene"
end function

' Swap the active child for a fresh instance of `name` with `itemId`
' pre-set. Does NOT touch the back-stack — both forward navigation
' and Back-pop funnel through here for the actual mount.
sub mountScene(name as String, itemId as String)
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
    if itemId <> "" and child.hasField("itemId")
        child.itemId = itemId
    end if
    m.currentChild = child
    m.content.appendChild(child)
    child.setFocus(true)
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
    mountScene(parent.name, parent.itemId)
    return true
end function
