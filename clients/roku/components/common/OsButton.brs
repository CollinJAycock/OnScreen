' See OsButton.xml.

sub init()
    m.bg = m.top.findNode("bg")
    m.label = m.top.findNode("label")
    m.top.focusable = true
    m.top.observeField("focusedChild", "onFocusChanged")
    layout()
end sub

sub layout()
    if m.top.size = "small"
        m.label.font = "font:SmallBoldSystemFont"
        pad = 28
    else
        m.label.font = "font:MediumBoldSystemFont"
        pad = 36
    end if
    m.label.text = m.top.text
    ' The label's natural width, before it's given a box.
    m.label.width = 0
    w = m.label.boundingRect().width + pad * 2
    if m.top.minWidth > 0 and w < m.top.minWidth then w = m.top.minWidth
    if m.top.maxWidth > 0 and w > m.top.maxWidth then w = m.top.maxWidth
    h = m.top.height
    m.bg.width = w
    m.bg.height = h
    m.label.width = w - pad
    m.label.height = h
    m.label.translation = [pad / 2, 0]
    m.top.width = w
    paint()
end sub

sub onFocusChanged()
    paint()
end sub

' Focused: the brand violet with white text. Otherwise a light glass
' over whatever is behind (the title page's art, the dark screens).
sub paint()
    if m.top.hasFocus()
        m.bg.blendColor = "0x7C6AF7FF"
        m.label.color = "0xFFFFFFFF"
    else
        m.bg.blendColor = "0xFFFFFF26"
        m.label.color = "0xE8E8EEFF"
    end if
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if press and key = "OK"
        m.top.buttonSelected = true
        return true
    end if
    return false
end function
