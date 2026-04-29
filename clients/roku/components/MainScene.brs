' MainScene controller. Decides on launch which sub-scene to show
' (Setup → Login → Home) and exposes a navigateTo() helper sub-
' scenes call when they finish their step.
'
' The scene graph itself is declared in MainScene.xml; this file
' wires behaviour. Keep view structure in the XML, behaviour here.

sub init()
    m.content = m.top.findNode("content")
    m.currentChild = invalid

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
sub navigateToWithItem(name as String, itemId as String)
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
