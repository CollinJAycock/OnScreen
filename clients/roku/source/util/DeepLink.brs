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
