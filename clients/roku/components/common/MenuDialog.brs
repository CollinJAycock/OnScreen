' Small shared menu / message dialog for the browse + detail scenes. A
' scene includes this file and implements:
'
'   sub onMenuChoice(menuId as String, index as Integer)
'       index is the chosen button (0-based), or -1 when the user backed
'       out. The dialog is already closed; the scene restores its own
'       focus (or opens the next menu).
'   function getMainScene() as Object
'
' Menu_Show(id, title, message, buttons) puts a Dialog on the root Scene
' (the one dialog slot SceneGraph gives a channel). Only the newest dialog
' reports back: a replaced dialog's late wasClosed is ignored, so a
' handler can chain straight into a confirm / result dialog.

sub Menu_Show(menuId as String, title as String, message as String, buttons as Object)
    dlg = createObject("roSGNode", "Dialog")
    dlg.title = title
    dlg.message = message
    dlg.buttons = buttons
    m.menuDialog = dlg
    m.menuId = menuId
    dlg.observeField("buttonSelected", "onMenuDialogButton")
    dlg.observeField("wasClosed", "onMenuDialogClosed")
    scene = getMainScene()
    if scene <> invalid then scene.dialog = dlg
end sub

' A message with a single OK. Reports back as index 0 (OK) or -1 (back).
sub Menu_ShowMessage(menuId as String, title as String, message as String)
    Menu_Show(menuId, title, message, ["OK"])
end sub

function Menu_IsOpen() as Boolean
    return m.menuDialog <> invalid
end function

sub onMenuDialogButton(evt as Object)
    dlg = evt.getRoSGNode()
    if m.menuDialog = invalid or not dlg.isSameNode(m.menuDialog) then return
    Menu_Finish(dlg.buttonSelected)
end sub

sub onMenuDialogClosed(evt as Object)
    dlg = evt.getRoSGNode()
    if m.menuDialog = invalid or not dlg.isSameNode(m.menuDialog) then return
    Menu_Finish(-1)
end sub

sub Menu_Finish(index as Integer)
    dlg = m.menuDialog
    menuId = m.menuId
    m.menuDialog = invalid
    m.menuId = ""
    if dlg <> invalid then dlg.close = true
    onMenuChoice(menuId, index)
end sub
