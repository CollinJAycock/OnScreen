' Unit tests for source/util/DeepLink.brs.

sub Main()
    testTransfer()
    id = "3f2b8c1e-9a4d-4e5f-8b6a-1c2d3e4f5a6b"

    dl = DeepLink_Parse({ contentId: id, mediaType: "movie" })
    runStr("movie: id", dl.contentId, id)
    runBool("movie: plays", dl.play, true)
    runBool("episode plays", DeepLink_Parse({ contentId: id, mediaType: "episode" }).play, true)
    runBool("shortFormVideo plays (case-insensitive)", DeepLink_Parse({ contentId: id, mediaType: "shortFormVideo" }).play, true)
    runBool("tvSpecial plays", DeepLink_Parse({ contentId: id, mediaType: "tvSpecial" }).play, true)
    runBool("series opens the page", DeepLink_Parse({ contentId: id, mediaType: "series" }).play, false)
    runBool("season opens the page", DeepLink_Parse({ contentId: id, mediaType: "season" }).play, false)
    runBool("live opens the page", DeepLink_Parse({ contentId: id, mediaType: "live" }).play, false)
    runBool("no mediaType opens the page", DeepLink_Parse({ contentId: id }).play, false)
    runBool("a non-string mediaType opens the page", DeepLink_Parse({ contentId: id, mediaType: 7 }).play, false)
    runStr("upper-case id is folded", DeepLink_Parse({ contentId: UCase(id), mediaType: "movie" }).contentId, id)

    runBool("no args", DeepLink_Parse(invalid) = invalid, true)
    runBool("not an AA", DeepLink_Parse("movie") = invalid, true)
    runBool("no contentId", DeepLink_Parse({ mediaType: "movie" }) = invalid, true)
    runBool("empty contentId", DeepLink_Parse({ contentId: "", mediaType: "movie" }) = invalid, true)
    runBool("numeric contentId", DeepLink_Parse({ contentId: 42, mediaType: "movie" }) = invalid, true)
    ' Ends up in an API path: nothing but a UUID gets through.
    runBool("path traversal", DeepLink_Parse({ contentId: "../../auth/logout", mediaType: "movie" }) = invalid, true)
    runBool("uuid with a suffix", DeepLink_Parse({ contentId: id + "/children", mediaType: "movie" }) = invalid, true)
    runBool("uuid with a query", DeepLink_Parse({ contentId: id + "?x=1", mediaType: "movie" }) = invalid, true)
    runBool("short id", DeepLink_Parse({ contentId: "3f2b8c1e", mediaType: "movie" }) = invalid, true)

    print "DONE: DeepLink_test"
end sub

sub runStr(name as String, actual as Dynamic, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + FormatJson(actual) + "]"
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub

sub testTransfer()
    id = "3f2b8c1e-9a4d-4e5f-8b6a-1c2d3e4f5a6b"
    t = DeepLink_FromTransfer({ ok: true, code: 200, data: { item_id: UCase(id), position_ms: 61500, target_client_name: "Roku" } })
    runStr("transfer: id", t.contentId, id)
    runStr("transfer: position", t.positionMs.ToStr(), "61500")
    runStr("transfer: no position", DeepLink_FromTransfer({ ok: true, data: { item_id: id } }).positionMs.ToStr(), "0")
    runStr("transfer: negative position", DeepLink_FromTransfer({ ok: true, data: { item_id: id, position_ms: -5 } }).positionMs.ToStr(), "0")
    runBool("transfer: nothing waiting (204)", DeepLink_FromTransfer({ ok: true, code: 204, data: invalid }) = invalid, true)
    runBool("transfer: failed", DeepLink_FromTransfer({ ok: false, code: 500, data: { item_id: id } }) = invalid, true)
    runBool("transfer: not an item id", DeepLink_FromTransfer({ ok: true, data: { item_id: "../admin" } }) = invalid, true)
    runBool("transfer: invalid", DeepLink_FromTransfer(invalid) = invalid, true)
    runStr("transfer: client name", DeepLink_ClientName(), "Roku")
end sub
