' Seek-bar thumbnails (trickplay) for the Roku player.
'
' Roku firmware renders trick-play thumbnails itself, on the Video node's
' built-in seek bar, from a BIF file named on the content node
' (HDBifUrl / SDBifUrl / FHDBifUrl). The server builds that BIF from the
' item's sprite sheets: GET /trickplay/{itemId}/index.bif, authenticated
' with the purpose=asset token in ?token= (the Video node can't send an
' Authorization header, and the server rejects the access token in a URL).
' Items without generated thumbnails answer 404 and the bar simply shows
' none. Pure helpers — tests/Trickplay_test.brs.

' The BIF for an item's thumbnails. One 320 px-wide BIF serves every
' display mode, so the same URL goes in all three fields.
function Trickplay_BifUrl(serverUrl as String, itemId as String, assetToken as String) as String
    return serverUrl + "/trickplay/" + itemId + "/index.bif?token=" + assetToken
end function

' Whether the item's thumbnails line up with what the Video node plays.
' The server cuts them from the item's first file (with the other parts
' when that file is part of a stacked movie), on that file's timeline:
'   - another version (an edition, a different cut) may run differently,
'     so it gets none;
'   - a re-issued session whose clock starts mid-item (streamOffsetMs > 0)
'     would show every thumbnail at the wrong time.
' Audio (tracks, audiobooks, podcasts) never has thumbnails; skipping it
' saves the firmware a 404 per track.
' versionFiles is the played version's files (Versions_Pick(...).files).
function Trickplay_Applies(itemType as Dynamic, itemFiles as Dynamic, versionFiles as Dynamic, streamOffsetMs as Dynamic) as Boolean
    if itemType <> invalid
        if itemType = "track" or itemType = "audiobook" or itemType = "audiobook_chapter" or itemType = "podcast_episode" then return false
    end if
    if streamOffsetMs = invalid or streamOffsetMs <> 0 then return false
    if itemFiles = invalid or type(itemFiles) <> "roArray" or itemFiles.Count() = 0 then return false
    if versionFiles = invalid or type(versionFiles) <> "roArray" then return false
    first = itemFiles[0]
    if first = invalid or type(first) <> "roAssociativeArray" or first.id = invalid then return false
    for each f in versionFiles
        if f <> invalid and type(f) = "roAssociativeArray" and f.id <> invalid
            if f.id = first.id then return true
        end if
    end for
    return false
end function

' Point content's trick-play bar at the item's BIF, when it applies and an
' asset token is on hand. content is the ContentNode about to be handed to
' the Video node.
sub Trickplay_AttachBif(content as Object, serverUrl as String, itemId as String, assetToken as Dynamic, applies as Boolean)
    if not applies then return
    if assetToken = invalid or assetToken = "" or itemId = "" then return
    url = Trickplay_BifUrl(serverUrl, itemId, assetToken)
    content.HDBifUrl = url
    content.SDBifUrl = url
    content.FHDBifUrl = url
end sub
