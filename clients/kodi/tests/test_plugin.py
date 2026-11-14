"""The plugin router, the play resolver and the service, run against the
Kodi stand-ins (tests/kodistubs) and the fake server (conftest.py)."""

import json
import time
import urllib.parse

from conftest import sign_in

BASE = "plugin://plugin.video.onscreen/"


def run(argv_query, resume=""):
    from resources.lib.kodi import plugin

    argv = [BASE, "1", "?" + argv_query if argv_query else ""]
    if resume:
        argv.append(resume)
    plugin.run(argv)


def run_action(argv_query):
    """How Kodi runs a non-folder, non-playable item: handle -1."""
    from resources.lib.kodi import plugin

    plugin.run([BASE, "-1", "?" + argv_query])


def labels(kodi):
    return [li.label for _, li, _ in kodi["plugin"].directory]


def params(url):
    return dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(url).query))


def test_root_signed_out_offers_sign_in(kodi, server):
    run("")
    assert labels(kodi) == ["Sign in", "Settings"]
    assert [folder for _, _, folder in kodi["plugin"].directory] == [False, False]
    assert server.requests == []


def test_root_signed_in(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/hub")] = (
        200,
        {
            "data": {
                "continue_watching": [{"id": "e1", "type": "episode", "title": "Ep"}],
                "next_up": [],
                "recently_added": [{"id": "m1", "type": "movie", "title": "M"}],
                "collection_rows": [],
            }
        },
    )
    server.routes[("GET", "/api/v1/libraries")] = (
        200,
        {
            "data": [
                {"id": "L1", "name": "Movies", "type": "movie"},
                {"id": "L2", "name": "Photos", "type": "photo"},
                {"id": "L3", "name": "Music", "type": "music"},
            ]
        },
    )
    run("content_type=video")
    assert labels(kodi) == [
        "Continue Watching",
        "Recently Added",
        "Movies",
        "Watchlist",
        "Favorites",
        "Search",
        "Settings",
    ]
    lib_url = kodi["plugin"].directory[2][0]
    assert params(lib_url) == {"action": "library", "id": "L1", "kind": "movie", "s": "video"}
    assert kodi["plugin"].directory[-1][1].label2 == "Signed in as alice"
    hub_req = server.calls("GET", "/api/v1/hub")[0]
    assert hub_req.headers["Authorization"] == "Bearer acc"
    assert "vodRemux=1" in hub_req.headers["X-Client-Capabilities"]
    assert hub_req.headers["X-Device-Name"].startswith("Kodi")


def test_library_page_with_next_page(kodi, server):
    sign_in(kodi)
    kodi["addon"].settings["page_size"] = 50
    items = [
        {"id": "m%d" % i, "type": "movie", "title": "Movie %d" % i, "poster_path": "M/%d.jpg" % i} for i in range(50)
    ]
    server.routes[("GET", "/api/v1/libraries/L1/items")] = (200, {"data": items, "meta": {"total": 120}})
    kodi["gui"].Window().setProperty("onscreen.artport", "52872")
    run("action=items&id=L1&kind=movie&s=video")
    d = kodi["plugin"].directory
    assert len(d) == 51
    first_url, first_li, folder = d[0]
    assert not folder and first_li.props["IsPlayable"] == "true"
    assert params(first_url) == {"action": "play", "id": "m0", "s": "video"}
    assert first_li.art["poster"] == "http://127.0.0.1:52872/artwork/M/0.jpg?w=500"
    nxt_url, nxt_li, nxt_folder = d[-1]
    assert nxt_folder and nxt_li.label == "Next page (50 of 120)"
    assert params(nxt_url)["offset"] == "50"
    assert kodi["plugin"].content == ["movies"]
    assert server.requests[-1].query["limit"] == "50"


def test_artwork_without_the_proxy_uses_the_asset_token(kodi, server):
    sign_in(kodi, asset="AT")
    server.routes[("GET", "/api/v1/items/s1/children")] = (
        200,
        {"data": [{"id": "x", "type": "season", "index": 1, "poster_path": "TV/S/p.jpg"}]},
    )
    run("action=children&id=s1")
    li = kodi["plugin"].directory[0][1]
    assert li.art["poster"] == server.url + "/artwork/TV/S/p.jpg?w=500&token=AT"
    assert li.label == "Season 1"


ITEM = {
    "id": "m1",
    "type": "movie",
    "title": "Sintel",
    "duration_ms": 888000,
    "view_offset_ms": 336000,
    "poster_path": "M/s.jpg",
    "files": [
        {
            "id": "f1",
            "stream_token": "ST",
            "container": "matroska",
            "video_codec": "hevc",
            "duration_ms": 888000,
            "subtitle_streams": [{"index": 3, "language": "en", "image_based": False}],
            "external_subtitles": [{"id": "x1", "language": "es"}],
        }
    ],
}


def resolved(kodi):
    ok, li = kodi["plugin"].resolved[-1]
    return ok, li


def playback_prop(kodi):
    raw = kodi["gui"].Window().getProperty("onscreen.playback")
    return json.loads(raw) if raw else None


def test_direct_play_resumes_when_kodi_says_so(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": ITEM})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (
        200,
        {"data": {"decision": "directPlay", "file_id": "f1"}},
    )
    server.routes[("GET", "/media/subtitles/f1/external/x1")] = (
        200,
        b"WEBVTT\n\n00:00.000 --> 00:01.000\nHola\n",
        "text/vtt",
    )
    run("action=play&id=m1&r=1", "resume:true")
    ok, li = resolved(kodi)
    assert ok and li.path == server.url + "/media/stream/f1?token=ST"
    assert li.video.fields["ResumePoint"] == (336.0, 888.0)
    assert len(li.subtitles) == 1 and li.subtitles[0].endswith("External 1.es.vtt")
    prop = playback_prop(kodi)
    assert (
        prop["item_id"] == "m1"
        and prop["offset_ms"] == 0
        and prop["decision"] == "directPlay"
        and prop["session"] is None
    )
    assert server.calls("POST", "/api/v1/items/m1/playback-decision")[0].json() == {"file_id": "f1"}


def test_play_from_beginning_after_kodis_question(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": ITEM})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (200, {"data": {"decision": "directPlay"}})
    run("action=play&id=m1&r=1", "resume:false")
    ok, li = resolved(kodi)
    assert ok and "ResumePoint" not in li.video.fields
    assert not [s for s in kodi["gui"].shown if s[0] == "contextmenu"]


def test_asks_about_resume_when_kodi_did_not(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": ITEM})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (200, {"data": {"decision": "directPlay"}})
    kodi["gui"].answers["contextmenu"].append(0)
    run("action=play&id=m1", "resume:false")
    asked = [s for s in kodi["gui"].shown if s[0] == "contextmenu"][0]
    assert asked[1] == ["Resume from 5:36", "Play from the beginning"]
    assert resolved(kodi)[1].video.fields["ResumePoint"] == (336.0, 888.0)


def test_remux_starts_a_session_at_the_resume_point(kodi, server):
    sign_in(kodi)
    kodi["xbmc"].conditions["System.AddonIsEnabled(inputstream.adaptive)"] = True
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": ITEM})
    server.routes[("GET", "/api/v1/users/me/preferences")] = (200, {"data": {"preferred_subtitle_lang": "en"}})
    server.routes[("GET", "/media/subtitles/f1/3")] = (200, b"WEBVTT\n", "text/vtt")
    server.routes[("GET", "/media/subtitles/f1/external/x1")] = (200, b"WEBVTT\n", "text/vtt")
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (200, {"data": {"decision": "directStream"}})
    server.routes[("POST", "/api/v1/items/m1/transcode")] = (
        200,
        {
            "data": {
                "session_id": "S1",
                "token": "TK",
                "playlist_url": "/api/v1/transcode/sessions/S1/playlist.m3u8?token=TK",
                "start_offset_sec": 334.5,
            }
        },
    )
    run("action=play&id=m1&r=1", "resume:true")
    body = server.calls("POST", "/api/v1/items/m1/transcode")[0].json()
    assert body["video_copy"] is True and body["position_ms"] == 336000 and body["segment_container"] == "ts"
    ok, li = resolved(kodi)
    assert ok and li.path == server.url + "/api/v1/transcode/sessions/S1/playlist.m3u8?token=TK"
    assert li.mime == "application/vnd.apple.mpegurl"
    assert li.props["inputstream"] == "inputstream.adaptive"
    assert li.props["inputstream.adaptive.play_timeshift_buffer"] == "true"
    assert li.video.fields["ResumePoint"][0] == 1.5  # from the keyframe to the resume point
    assert len(li.subtitles) == 2
    prop = playback_prop(kodi)
    assert (
        prop["offset_ms"] == 334500
        and prop["session"] == {"id": "S1", "token": "TK"}
        and prop["decision"] == "directStream"
    )


def test_full_timeline_session_seeks_natively(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": ITEM})
    server.routes[("GET", "/api/v1/users/me/preferences")] = (200, {"data": {}})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (200, {"data": {"decision": "directStream"}})
    server.routes[("POST", "/api/v1/items/m1/transcode")] = (
        200,
        {
            "data": {
                "session_id": "S1",
                "token": "TK",
                "playlist_url": "/p.m3u8",
                "start_offset_sec": 0,
                "full_timeline": True,
            }
        },
    )
    run("action=play&id=m1&r=1", "resume:true")
    ok, li = resolved(kodi)
    assert "inputstream" not in li.props  # Kodi's own HLS demuxer plays a VOD playlist
    assert li.video.fields["ResumePoint"] == (336.0, 888.0)
    assert playback_prop(kodi)["offset_ms"] == 0


def test_too_many_streams_refuses(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": dict(ITEM, view_offset_ms=0)})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (
        429,
        {"error": {"code": "TOO_MANY_SESSIONS", "message": "You're already watching 2 streams."}},
    )
    run("action=play&id=m1")
    assert resolved(kodi)[0] is False
    assert ("ok", "OnScreen", "You're already watching 2 streams.") in kodi["gui"].shown
    assert playback_prop(kodi) is None


def test_parental_limit_reason_is_worded(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": dict(ITEM, view_offset_ms=0)})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (
        403,
        {"error": {"code": "PARENTAL_LIMIT", "message": "daily_limit_reached"}},
    )
    run("action=play&id=m1")
    assert ("ok", "OnScreen", "This account has used up today's watch time.") in kodi["gui"].shown


def test_version_picker(kodi, server):
    sign_in(kodi)
    item = dict(
        ITEM,
        view_offset_ms=0,
        files=[
            {"id": "a", "resolution_h": 2160, "video_codec": "hevc"},
            {"id": "b", "resolution_h": 1080, "video_codec": "h264", "stream_token": "B"},
        ],
    )
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": item})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (200, {"data": {"decision": "directPlay"}})
    kodi["gui"].answers["select"].append(1)
    run("action=play&id=m1&choose=1")
    sel = [s for s in kodi["gui"].shown if s[0] == "select"][0]
    assert sel[2] == ["4K · HEVC", "1080p · H.264"]
    assert resolved(kodi)[1].path.endswith("/media/stream/b?token=B")


def test_mark_watched_action(kodi, server):
    sign_in(kodi)
    server.routes[("POST", "/api/v1/items/e1/watched")] = (204, None)
    run("action=watched&id=e1&on=1")
    assert server.calls("POST", "/api/v1/items/e1/watched")
    assert "Container.Refresh" in kodi["xbmc"].builtins


def test_search_prompts_then_updates_the_container(kodi, server):
    sign_in(kodi)
    kodi["gui"].answers["input"].append("sintel")
    run_action("action=search&s=video")  # an action item: no directory handle
    assert kodi["plugin"].ended == []
    cmd = kodi["xbmc"].builtins[-1]
    assert cmd.startswith("Container.Update(" + BASE) and cmd.endswith(")")
    assert params(cmd[len("Container.Update(") : -1]) == {"action": "search", "q": "sintel", "s": "video"}
    server.routes[("GET", "/api/v1/search")] = (200, {"data": [{"id": "m1", "type": "movie", "title": "Sintel"}]})
    kodi["plugin"].reset()
    run("action=search&q=sintel&s=video")
    assert labels(kodi) == ["Sintel"] and server.requests[-1].query["q"] == "sintel"


def test_expired_session_notifies(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/libraries/L1/items")] = (401, {"error": {"code": "UNAUTHORIZED"}})
    server.routes[("POST", "/api/v1/auth/refresh")] = (401, {"error": {"code": "UNAUTHORIZED"}})
    run("action=items&id=L1")
    assert ("notification", "Your sign-in has expired. Sign in again.") in kodi["gui"].shown
    assert kodi["plugin"].ended == [False]


def test_service_reports_progress_and_cleans_up(kodi, server):
    from resources.lib.kodi import service

    sign_in(kodi)
    progress = []
    server.routes[("PUT", "/api/v1/items/m1/progress")] = lambda req: progress.append(req.json()) or (204, None)
    server.routes[("DELETE", "/api/v1/transcode/sessions/S1")] = (204, None)
    url = server.url + "/p.m3u8"
    kodi["gui"].Window().setProperty(
        "onscreen.playback",
        json.dumps(
            {
                "item_id": "m1",
                "file_id": "f1",
                "url": url,
                "duration_ms": 888000,
                "offset_ms": 334500,
                "decision": "directStream",
                "session": {"id": "S1", "token": "TK"},
            }
        ),
    )
    svc = service.Service()
    svc._worker.start()
    svc.player.is_playing = True
    svc.player.playing_file = url
    svc.player.time = 10.0
    svc.adopt()
    assert kodi["gui"].Window().getProperty("onscreen.playback") == ""
    svc.player.time = 500.0
    svc.finish(ended=True)
    svc.jobs.put(None)
    svc._worker.join(10)
    assert [p["state"] for p in progress] == ["playing", "stopped"]
    assert progress[0]["view_offset_ms"] == 344500
    assert progress[1]["view_offset_ms"] == 344500  # stopped early, not "ended" at 888 s
    assert progress[0]["client_name"].startswith("Kodi") and progress[0]["file_id"] == "f1"
    stop = server.calls("DELETE", "/api/v1/transcode/sessions/S1")[0]
    # The route wants the user's bearer (Kodi 21 run: without it, 401 and
    # the ffmpeg ran on until the idle sweep); ?token= revokes the segments.
    assert stop.query == {"token": "TK"} and stop.headers["Authorization"] == "Bearer acc"


def test_service_end_of_stream_marks_the_end(kodi, server):
    from resources.lib.kodi import service

    sign_in(kodi)
    progress = []
    server.routes[("PUT", "/api/v1/items/m1/progress")] = lambda req: progress.append(req.json()) or (204, None)
    url = server.url + "/media/stream/f1?token=ST"
    kodi["gui"].Window().setProperty(
        "onscreen.playback",
        json.dumps({"item_id": "m1", "file_id": "f1", "url": url, "duration_ms": 888000, "offset_ms": 0}),
    )
    svc = service.Service()
    svc._worker.start()
    svc.player.is_playing = True
    svc.player.playing_file = url
    svc.player.time = 880.0
    svc.adopt()
    svc.tick()
    svc.finish(ended=True)
    svc.jobs.put(None)
    svc._worker.join(10)
    assert progress[-1] == dict(progress[-1], state="stopped", view_offset_ms=888000)


def test_service_ignores_other_playback(kodi, server):
    from resources.lib.kodi import service

    kodi["gui"].Window().setProperty("onscreen.playback", json.dumps({"item_id": "m1", "url": "http://x/a"}))
    svc = service.Service()
    svc.player.is_playing = True
    svc.player.playing_file = "/home/me/local.mkv"
    svc.adopt()
    assert svc.current is None
    assert kodi["gui"].Window().getProperty("onscreen.playback")  # left for its own playback


def test_service_stops_the_player_on_admin_stop(kodi, server):
    from resources.lib.kodi import service

    sign_in(kodi)
    server.routes[("PUT", "/api/v1/items/m1/progress")] = (
        403,
        {"error": {"code": "PLAYBACK_STOPPED", "message": "Stopped by an admin: maintenance"}},
    )
    url = server.url + "/media/stream/f1"
    kodi["gui"].Window().setProperty(
        "onscreen.playback", json.dumps({"item_id": "m1", "url": url, "duration_ms": 1000, "offset_ms": 0})
    )
    svc = service.Service()
    svc._worker.start()
    svc.player.is_playing = True
    svc.player.playing_file = url
    svc.adopt()
    deadline = time.time() + 5
    while svc.player.is_playing and time.time() < deadline:
        time.sleep(0.05)
    svc.jobs.put(None)
    svc._worker.join(5)
    assert svc.player.is_playing is False
    assert ("ok", "OnScreen", "Stopped by an admin: maintenance") in kodi["gui"].shown


def test_season_listing_gets_season_and_show_from_the_url(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/items/s1/children")] = (
        200,
        {"data": [{"id": "e1", "type": "episode", "title": "Pilot", "index": 1}]},
    )
    run("action=children&id=s1&season=2&show=Kodi+Show&s=video")
    li = kodi["plugin"].directory[0][1]
    assert (
        li.video.fields["Season"] == 2
        and li.video.fields["Episode"] == 1
        and li.video.fields["TvShowTitle"] == "Kodi Show"
    )
    assert kodi["plugin"].content == ["episodes"]
