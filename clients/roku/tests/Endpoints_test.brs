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
    runCase("playback decision", ApiItemPlaybackDecision(id), "/api/v1/items/" + id + "/playback-decision")
    runCase("optimized stream path", AssetOptimizedPath("/media/optimized/v-1", "tok"), "/media/optimized/v-1?token=tok")
    runCase("optimized stream path with a query", AssetOptimizedPath("/media/optimized/v-1?x=1", "tok"), "/media/optimized/v-1?x=1&token=tok")
    runCase("watchlist", ApiWatchlist("on_hold"), "/api/v1/watchlist?status=on_hold&sort=recent&limit=200")
    runCase("watchlist counts", ApiWatchlistCounts(), "/api/v1/watchlist/counts")
    runCase("watch status", ApiItemWatchStatus(id), "/api/v1/items/" + id + "/watch-status")

    print "DONE: Endpoints_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
