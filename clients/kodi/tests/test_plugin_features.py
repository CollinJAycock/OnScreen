"""The 0.2 features against the Kodi stand-ins and the fake server: track
choices, burn-in, explicit start, photos, Live TV, Up Next and "Play on…"."""

import base64
import json
import urllib.parse

from conftest import sign_in
from test_plugin import playback_prop, resolved, run

FILE = {
    "id": "f1",
    "stream_token": "ST",
    "container": "matroska",
    "video_codec": "h264",
    "duration_ms": 600000,
    "audio_streams": [
        {"index": 1, "codec": "ac3", "channels": 6, "language": "eng"},
        {"index": 2, "codec": "ac3", "channels": 6, "language": "spa"},
    ],
    "subtitle_streams": [
        {"index": 3, "codec": "subrip", "language": "eng"},
        {"index": 4, "codec": "hdmv_pgs_subtitle", "language": "spa", "image_based": True},
        {"index": 5, "codec": "subrip", "language": "spa"},
    ],
}
ITEM = {"id": "m1", "type": "movie", "title": "Two Tracks", "duration_ms": 600000, "view_offset_ms": 0, "files": [FILE]}


def setup(server, decision, prefs=None, session=None):
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": ITEM})
    server.routes[("POST", "/api/v1/items/m1/playback-decision")] = (200, {"data": {"decision": decision}})
    server.routes[("GET", "/api/v1/users/me/preferences")] = (200, {"data": prefs or {}})
    for idx in (3, 5):
        server.routes[("GET", "/media/subtitles/f1/%d" % idx)] = (200, b"WEBVTT\n", "text/vtt")
    server.routes[("POST", "/api/v1/items/m1/transcode")] = session or (
        200,
        {"data": {"session_id": "S", "token": "T", "playlist_url": "/p.m3u8", "full_timeline": True}},
    )


def test_direct_play_switches_to_the_preferred_language(kodi, server):
    sign_in(kodi)
    setup(server, "directPlay", {"preferred_audio_lang": "es", "preferred_subtitle_lang": "es"})
    run("action=play&id=m1")
    assert resolved(kodi)[0]
    # Kodi lists the file's own tracks: Spanish audio is the second; of the
    # two Spanish subtitles the text one (third stream) beats the PGS one.
    assert playback_prop(kodi)["select"] == {"audio": 1, "subtitle": 2, "subtitles_off": False}


FOREIGN = {"preferred_audio_lang": "spa", "preferred_subtitle_lang": "en", "subtitle_mode": "foreign"}


def test_foreign_audio_turns_on_the_full_track(kodi, server):
    sign_in(kodi)
    setup(server, "directPlay", FOREIGN)
    run("action=play&id=m1")
    prop = playback_prop(kodi)
    # Spanish audio isn't in the subtitle language: the English text track.
    assert prop["select"] == {"audio": 1, "subtitle": 0, "subtitles_off": False}
    # A switch to the English audio in Kodi's menu turns it off again.
    assert prop["repick"] == {
        "by_audio": {"0": {"subtitle": None, "subtitles_off": True}, "1": {"subtitle": 0, "subtitles_off": False}},
        "audio": 1,
    }


def test_audio_in_the_subtitle_language_keeps_them_off(kodi, server):
    sign_in(kodi)
    setup(server, "directPlay", dict(FOREIGN, preferred_audio_lang="eng"))
    run("action=play&id=m1")
    prop = playback_prop(kodi)
    assert prop["select"] == {"audio": 0, "subtitle": None, "subtitles_off": False}  # Kodi's own (forced only)
    assert prop["repick"]["audio"] == 0


def test_older_server_forced_switch_still_works(kodi, server):
    sign_in(kodi)
    setup(
        server,
        "directPlay",
        {"preferred_audio_lang": "spa", "preferred_subtitle_lang": "en", "forced_subtitles_only": True},
    )
    run("action=play&id=m1")
    prop = playback_prop(kodi)
    assert prop["select"]["subtitle"] is None and prop["repick"] is None  # forced only: no English forced track


def test_no_repick_in_a_session_or_with_options(kodi, server):
    sign_in(kodi)
    setup(server, "directStream", FOREIGN)
    run("action=play&id=m1")
    prop = playback_prop(kodi)
    assert prop["repick"] is None
    assert prop["select"] == {"audio": None, "subtitle": 0, "subtitles_off": False}  # the English file
    kodi["gui"].answers["select"].extend([1, 1])  # audio: Spanish; subtitles: English
    setup(server, "directPlay", FOREIGN)
    run("action=play&id=m1&options=1")
    assert playback_prop(kodi)["repick"] is None


def test_service_follows_audio_switches_until_the_viewer_picks(kodi, server):
    from resources.lib.kodi import service

    sign_in(kodi)
    setup(server, "directPlay", FOREIGN)
    run("action=play&id=m1")
    url = playback_prop(kodi)["url"]
    xbmc = kodi["xbmc"]
    svc = service.Service()
    svc.player.is_playing = True
    svc.player.playing_file = url
    svc.adopt()
    assert svc.player.switched == [("audio", 1), ("subtitle", 0), ("show", True)]
    svc.player.switched = []

    def state(audio, on, sub=None):
        xbmc.player_props = {
            "currentaudiostream": {"index": audio},
            "subtitleenabled": on,
            "currentsubtitle": {"index": sub} if sub is not None else {},
        }
        svc.av_changed = True
        svc.tick()

    state(1, True, 0)  # Kodi shows what the service set
    assert svc.player.switched == []
    state(0, True, 0)  # the viewer picks the English audio
    assert svc.player.switched == [("show", False)]
    state(0, False)  # the switch lands
    state(1, False)  # back to Spanish: the full English track again
    assert svc.player.switched == [("show", False), ("subtitle", 0), ("show", True)]
    state(1, True, 0)
    svc.repick["settle_until"] = 0
    state(1, True, 2)  # the viewer picks another subtitle themselves
    assert svc.repick is None
    svc.player.switched = []
    state(0, True, 2)  # later audio switches leave it alone
    assert svc.player.switched == []
    svc.finish(ended=False)


def test_session_gets_the_audio_track_and_the_subtitle_file(kodi, server):
    sign_in(kodi)
    setup(server, "directStream", {"preferred_audio_lang": "spa", "preferred_subtitle_lang": "es"})
    run("action=play&id=m1")
    body = server.calls("POST", "/api/v1/items/m1/transcode")[0].json()
    assert body["audio_stream_index"] == 1 and "burn_subtitle_stream_index" not in body
    ok, li = resolved(kodi)
    # Text tracks only (the PGS one can't become a file): Spanish first.
    assert [p.rsplit("\\", 1)[-1].rsplit("/", 1)[-1] for p in li.subtitles] == ["Track 3.spa.vtt", "Track 1.eng.vtt"]
    assert playback_prop(kodi)["select"] == {"audio": None, "subtitle": 0, "subtitles_off": False}


def test_play_with_options_burns_in_a_picture_subtitle(kodi, server):
    sign_in(kodi)
    setup(server, "directStream")
    kodi["gui"].answers["select"].extend([1, 2])  # audio: Spanish; subtitles: the PGS track
    run("action=play&id=m1&options=1")
    sels = [s for s in kodi["gui"].shown if s[0] == "select"]
    assert sels[0][2] == ["English (AC3 5.1)", "Spanish (AC3 5.1)"]
    assert sels[1][2] == ["Off", "English", "Spanish (burned in)", "Spanish"]
    body = server.calls("POST", "/api/v1/items/m1/transcode")[0].json()
    assert body["burn_subtitle_stream_index"] == 4 and body["video_copy"] is False and body["audio_stream_index"] == 1
    prop = playback_prop(kodi)
    assert prop["decision"] == "transcode" and prop["select"]["subtitle"] is None


def test_refused_burn_in_plays_without_it(kodi, server):
    sign_in(kodi)
    calls = []

    def start(req):
        calls.append(req.json())
        if len(calls) == 1:
            return 503, {"error": {"code": "BURN_IN_UNAVAILABLE", "message": "no worker"}}
        return 200, {"data": {"session_id": "S", "token": "T", "playlist_url": "/p.m3u8", "full_timeline": True}}

    setup(server, "directStream", session=start)
    kodi["gui"].answers["select"].extend([0, 2])
    run("action=play&id=m1&options=1")
    assert "burn_subtitle_stream_index" in calls[0] and "burn_subtitle_stream_index" not in calls[1]
    assert any(s[0] == "ok" and "can't burn" in s[2] for s in kodi["gui"].shown)
    assert resolved(kodi)[0]


def test_options_subtitles_off_in_a_direct_play(kodi, server):
    sign_in(kodi)
    setup(server, "directPlay")
    kodi["gui"].answers["select"].extend([0, 0])
    run("action=play&id=m1&options=1")
    assert playback_prop(kodi)["select"] == {"audio": 0, "subtitle": None, "subtitles_off": True}


def test_explicit_start_skips_the_resume_question(kodi, server):
    sign_in(kodi)
    setup(server, "directPlay")
    server.routes[("GET", "/api/v1/items/m1")] = (200, {"data": dict(ITEM, view_offset_ms=120000)})
    run("action=play&id=m1&start=90000")
    assert not [s for s in kodi["gui"].shown if s[0] == "contextmenu"]
    assert resolved(kodi)[1].video.fields["ResumePoint"] == (90.0, 600.0)


def test_growing_session_offers_inputstream_adaptive_once(kodi, server):
    sign_in(kodi)
    setup(server, "transcode", session=(200, {"data": {"session_id": "S", "token": "T", "playlist_url": "/p.m3u8"}}))
    run("action=play&id=m1")
    assert "InstallAddon(inputstream.adaptive)" in kodi["xbmc"].builtins
    assert kodi["addon"].settings["offer_adaptive"] is False
    kodi["xbmc"].builtins.clear()
    kodi["plugin"].reset()
    run("action=play&id=m1")
    assert "InstallAddon(inputstream.adaptive)" not in kodi["xbmc"].builtins  # once


def test_photos_list_the_images(kodi, server):
    sign_in(kodi, asset="AT")
    server.routes[("GET", "/api/v1/libraries")] = (
        200,
        {"data": [{"id": "P", "name": "Photos", "type": "photo"}, {"id": "M", "name": "Movies", "type": "movie"}]},
    )
    run("content_type=image")
    assert [li.label for _, li, _ in kodi["plugin"].directory] == ["Photos", "Settings"]
    kodi["plugin"].reset()
    server.routes[("GET", "/api/v1/libraries/P/items")] = (
        200,
        {
            "data": [{"id": "0ecfaca8-cdd7-44cb-a51e-9f75a90d4330", "type": "photo", "title": "IMG_1"}],
            "meta": {"total": 1},
        },
    )
    kodi["gui"].Window().setProperty("onscreen.artport", "52872")
    run("action=photos&id=P&s=image")
    url, li, folder = kodi["plugin"].directory[0]
    assert url == "http://127.0.0.1:52872/photo/0ecfaca8-cdd7-44cb-a51e-9f75a90d4330.jpg?w=3840" and not folder
    assert li.mime == "image/jpeg" and li.art["thumb"].endswith(".jpg?w=480")
    assert server.requests[-1].query["sort"] == "taken_at" and server.requests[-1].query["sort_dir"] == "desc"
    assert kodi["plugin"].content == ["images"]


def test_live_tv(kodi, server):
    sign_in(kodi, asset="AT")
    server.routes[("GET", "/api/v1/hub")] = (200, {"data": {}})
    server.routes[("GET", "/api/v1/libraries")] = (200, {"data": []})
    server.routes[("GET", "/api/v1/system/capabilities")] = (200, {"data": {"features": {"live_tv": True}}})
    run("content_type=video")
    labels = [li.label for _, li, _ in kodi["plugin"].directory]
    assert "Live TV" in labels
    kodi["plugin"].reset()
    server.routes[("GET", "/api/v1/tv/channels")] = (200, {"data": [{"id": "c1", "number": "2", "name": "Two"}]})
    server.routes[("GET", "/api/v1/tv/channels/now-next")] = (500, {"error": {"code": "X"}})  # guide down
    run("action=channels&s=video")
    url, li, _ = kodi["plugin"].directory[0]
    assert li.label == "2  Two" and li.props["IsPlayable"] == "true"
    kodi["plugin"].reset()
    run(urllib.parse.urlsplit(url).query)
    ok, li = resolved(kodi)
    assert ok and li.path == server.url + "/api/v1/tv/channels/c1/stream.m3u8?token=AT"
    assert li.mime == "application/vnd.apple.mpegurl"
    assert playback_prop(kodi) is None  # nothing to report progress for


def test_live_tv_hidden_without_the_feature(kodi, server):
    sign_in(kodi)
    server.routes[("GET", "/api/v1/hub")] = (200, {"data": {}})
    server.routes[("GET", "/api/v1/libraries")] = (200, {"data": []})
    server.routes[("GET", "/api/v1/system/capabilities")] = (200, {"data": {"features": {"live_tv": False}}})
    run("content_type=video")
    assert "Live TV" not in [li.label for _, li, _ in kodi["plugin"].directory]


def service_with(kodi, data, url):
    from resources.lib.kodi import service

    kodi["gui"].Window().setProperty("onscreen.playback", json.dumps(dict(data, url=url)))
    svc = service.Service()
    svc._worker.start()
    svc.player.is_playing = True
    svc.player.playing_file = url
    return svc


def finish(svc):
    svc.jobs.put(None)
    svc._worker.join(10)


def test_service_applies_the_track_choice(kodi, server):
    sign_in(kodi)
    server.routes[("PUT", "/api/v1/items/m1/progress")] = (204, None)
    svc = service_with(
        kodi, {"item_id": "m1", "select": {"audio": 1, "subtitle": 2, "subtitles_off": False}}, "http://x/a"
    )
    svc.adopt()
    finish(svc)
    assert svc.player.switched == [("audio", 1), ("subtitle", 2), ("show", True)]


def test_service_hands_up_next_the_next_episode(kodi, server):
    sign_in(kodi)
    kodi["xbmc"].conditions["System.AddonIsEnabled(service.upnext)"] = True
    server.routes[("PUT", "/api/v1/items/e1/progress")] = (204, None)
    server.routes[("GET", "/api/v1/items/s1")] = (
        200,
        {"data": {"id": "s1", "type": "season", "index": 1, "parent_id": "sh"}},
    )
    server.routes[("GET", "/api/v1/items/s1/children")] = (
        200,
        {
            "data": [
                {"id": "e1", "type": "episode", "index": 1},
                {"id": "e2", "type": "episode", "index": 2, "title": "Two"},
            ]
        },
    )
    server.routes[("GET", "/api/v1/items/sh")] = (200, {"data": {"id": "sh", "type": "show", "title": "Kodi Show"}})
    server.routes[("GET", "/api/v1/items/e1")] = (
        200,
        {"data": {"id": "e1", "type": "episode", "index": 1, "title": "One"}},
    )
    item = {"type": "episode", "index": 1, "parent_id": "s1", "grandparent_id": "sh"}
    svc = service_with(kodi, {"item_id": "e1", "item": item}, "http://x/e1")
    svc.adopt()
    finish(svc)
    sent = kodi["xbmc"].notifications
    assert len(sent) == 1 and sent[0]["message"] == "upnext_data"
    data = json.loads(base64.b64decode(sent[0]["data"][0]))
    assert data["current_episode"]["title"] == "One" and data["next_episode"]["title"] == "Two"
    assert data["next_episode"]["showtitle"] == "Kodi Show" and data["next_episode"]["season"] == 1
    assert "action=play" in data["play_url"] and "id=e2" in data["play_url"]


def test_service_skips_up_next_when_it_is_not_installed(kodi, server):
    sign_in(kodi)
    server.routes[("PUT", "/api/v1/items/e1/progress")] = (204, None)
    svc = service_with(kodi, {"item_id": "e1", "item": {"type": "episode", "parent_id": "s1"}}, "http://x/e1")
    svc.adopt()
    finish(svc)
    assert kodi["xbmc"].notifications == []


def test_play_on_transfer_starts_playback(kodi, server):
    from resources.lib.kodi import service

    sign_in(kodi)
    server.routes[("POST", "/api/v1/playback/transfer/claim")] = (
        200,
        {"data": {"item_id": "m1", "position_ms": 754000}},
    )
    service.Service().claim_transfer()
    claim = server.calls("POST", "/api/v1/playback/transfer/claim")[0]
    assert claim.json()["client_name"].startswith("Kodi")
    cmd = kodi["xbmc"].builtins[-1]
    assert cmd.startswith("PlayMedia(plugin://plugin.video.onscreen/?") and "id=m1" in cmd and "start=754000" in cmd


def test_play_on_nothing_waiting(kodi, server):
    from resources.lib.kodi import service

    sign_in(kodi)
    server.routes[("POST", "/api/v1/playback/transfer/claim")] = (204, None)
    service.Service().claim_transfer()
    assert kodi["xbmc"].builtins == []
