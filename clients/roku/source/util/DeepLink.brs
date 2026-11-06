' Roku deep links: launch args (Main's args) and roInput events carry a
' contentId and a mediaType (Roku's names: movie, episode, shortFormVideo,
' tvSpecial, special, season, series, live). Roku certification requires a
' channel to open the content a deep link names; MainScene routes the
' parsed link (openDeepLink).
'
' contentId is an OnScreen item id, a UUID. Anything else is ignored rather
' than handed on: it ends up in an API path, and a deep link can come from
' any app on the network (ECP's /launch and /input).

' { contentId, play } for a usable link, else invalid. play is true for the
' media types Roku expects to start playing at once; the rest (season,
' series, live, an unknown or missing type) open the title's page.
function DeepLink_Parse(args as Dynamic) as Dynamic
    if args = invalid or type(args) <> "roAssociativeArray" then return invalid
    id = args.contentId
    if type(id) <> "String" and type(id) <> "roString" then return invalid
    if not DeepLink_IsItemId(id) then return invalid
    mediaType = args.mediaType
    if type(mediaType) <> "String" and type(mediaType) <> "roString" then mediaType = ""
    return { contentId: LCase(id), play: DeepLink_Plays(mediaType) }
end function

function DeepLink_IsItemId(id as String) as Boolean
    re = CreateObject("roRegex", "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", "i")
    return re.IsMatch(id)
end function

function DeepLink_Plays(mediaType as String) as Boolean
    t = LCase(mediaType)
    return t = "movie" or t = "episode" or t = "shortformvideo" or t = "tvspecial" or t = "special"
end function

' ── "Play on this TV" (a transfer from another device) ─────────────

' This channel's client_name: what its progress reports carry, so it's
' the name the "Play on..." picker offers and a transfer targets.
function DeepLink_ClientName() as String
    return "Roku"
end function

' The transfer in a POST /playback/transfer/claim reply (ApiResult_From:
' 200 with data { item_id, position_ms, target_client_name }), as
' { contentId, positionMs }; invalid for a 204 (nothing waiting), a
' failure, or a reply that doesn't name an item.
function DeepLink_FromTransfer(res as Dynamic) as Dynamic
    if res = invalid or type(res) <> "roAssociativeArray" or res.ok <> true then return invalid
    d = res.data
    if d = invalid or type(d) <> "roAssociativeArray" then return invalid
    id = d.item_id
    if type(id) <> "String" and type(id) <> "roString" then return invalid
    if not DeepLink_IsItemId(id) then return invalid
    startMs = 0
    p = d.position_ms
    t = type(p)
    if t = "Integer" or t = "roInt" or t = "LongInteger" or t = "Float" or t = "Double"
        if p > 0 then startMs = Int(p)
    end if
    return { contentId: LCase(id), positionMs: startMs }
end function
