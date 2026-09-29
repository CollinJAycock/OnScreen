' Unit tests for the v2.5 API paths in source/api/Endpoints.brs, pinned
' against the Go router (internal/api/router.go) so a typo can't ship.

sub Main()
    id = "550e8400-e29b-41d4-a716-446655440000"
    runCase("watched", ApiItemWatched(id), "/api/v1/items/" + id + "/watched")
    runCase("dismiss continue watching", ApiItemDismissContinueWatching(id), "/api/v1/items/" + id + "/dismiss-continue-watching")
    runCase("up next", ApiItemUpNext(id), "/api/v1/items/" + id + "/up-next")
    runCase("issues", ApiItemIssues(id), "/api/v1/items/" + id + "/issues")
    runCase("progress", ApiItemProgress(id), "/api/v1/items/" + id + "/progress")
    runCase("stream path", AssetStreamPath("f-1", "tok"), "/media/stream/f-1?token=tok")
    runCase("stream url is origin + path", AssetStream("http://x", "f-1", "tok"), "http://x" + AssetStreamPath("f-1", "tok"))

    print "DONE: Endpoints_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
