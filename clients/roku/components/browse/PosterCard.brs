' PosterCard controller. Bound by RowList — `itemContent` field
' fires onItemContentChange whenever the parent assigns the
' ContentNode for this slot. Pull poster + title off the node and
' update the visuals.

sub init()
    m.poster = m.top.findNode("poster")
    m.title = m.top.findNode("title")
end sub

sub onItemContentChange()
    content = m.top.itemContent
    if content = invalid then return
    if content.HDPosterUrl <> invalid and content.HDPosterUrl <> ""
        m.poster.uri = content.HDPosterUrl
    else
        m.poster.uri = ""
    end if
    m.title.text = content.title
end sub
