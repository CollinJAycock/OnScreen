' PosterCard controller. Bound by RowList — `itemContent` field
' fires onItemContentChange whenever the parent assigns the
' ContentNode for this slot; `width` / `height` follow the row's
' item size. Pull poster + title + watch state off the node and
' update the visuals.

sub init()
    m.poster = m.top.findNode("poster")
    m.title = m.top.findNode("title")
    m.subtitle = m.top.findNode("subtitle")
    m.progressTrack = m.top.findNode("progressTrack")
    m.progressFill = m.top.findNode("progressFill")
    m.watchedBadge = m.top.findNode("watchedBadge")
    m.countBadge = m.top.findNode("countBadge")
    m.countLabel = m.top.findNode("countLabel")
    m.progressPct = 0
    layout()
end sub

sub onSizeChange()
    layout()
end sub

' Lay every piece out against the current slot size.
sub layout()
    w = m.top.width
    h = m.top.height
    if w <= 0 then w = 240
    if h <= 0 then h = 360
    m.poster.width = w
    m.poster.height = h
    m.poster.loadWidth = w
    m.poster.loadHeight = h
    m.progressTrack.width = w
    m.progressTrack.translation = [0, h - 8]
    m.progressFill.translation = [0, h - 8]
    m.progressFill.width = Int(w * m.progressPct / 100)
    m.watchedBadge.translation = [w - 44, 8]
    m.countBadge.translation = [w - 60, 8]
    m.title.width = w
    m.title.translation = [0, h + 5]
    m.subtitle.width = w
    m.subtitle.translation = [0, h + 37]
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

    subtitle = content.getField("cardSubtitle")
    if subtitle <> invalid and subtitle <> ""
        m.subtitle.text = subtitle
        m.subtitle.visible = true
    else
        m.subtitle.text = ""
        m.subtitle.visible = false
    end if

    ' Watch state. Fields are optional — a card without them (music,
    ' photos, older servers) shows no badge at all.
    badge = content.getField("watchBadge")
    if badge = invalid then badge = ""
    m.watchedBadge.visible = (badge = "watched")

    count = content.getField("unwatchedCount")
    if badge = "unwatched" and count <> invalid and count > 0
        if count > 99
            m.countLabel.text = "99+"
        else
            m.countLabel.text = count.ToStr()
        end if
        m.countBadge.visible = true
    else
        m.countBadge.visible = false
    end if

    pct = content.getField("progressPct")
    if badge = "progress" and pct <> invalid and pct > 0
        m.progressPct = pct
        m.progressFill.width = Int(m.poster.width * pct / 100)
        m.progressTrack.visible = true
        m.progressFill.visible = true
    else
        m.progressPct = 0
        m.progressTrack.visible = false
        m.progressFill.visible = false
    end if
end sub
