from resources.lib.onscreen import api, playback


def resp(status, data=None, code="", message=""):
    import json

    body = {"data": data} if data is not None else {}
    if code:
        body = {"error": {"code": code, "message": message}}
    return api.Response(status, json.dumps(body).encode(), "application/json")


def t(key, **p):
    return {"version_n": "Version {n}", "version_parts": "{version} · {count} parts"}[key].format(**p)


FILES = [
    {"id": "a", "edition": "Director's Cut", "container": "matroska", "resolution_h": 2160, "video_codec": "hevc"},
    {"id": "p2", "part_index": 2, "container": "avi", "video_codec": "h264", "resolution_h": 480},
    {"id": "p1", "part_index": 1, "container": "avi", "video_codec": "h264", "resolution_h": 480},
    {"id": "b", "container": "mov", "resolution_h": 1080, "video_codec": "h264", "hdr_type": "hdr10"},
]


def test_group_versions_stacks_parts_in_order():
    vs = playback.group_versions(FILES)
    assert [[f["id"] for f in v.files] for v in vs] == [["a"], ["p1", "p2"], ["b"]]
    assert vs[1].stacked and vs[1].primary["id"] == "p1"


def test_pick_version():
    assert playback.pick_version(FILES, "p2").primary["id"] == "p1"
    assert playback.pick_version(FILES, "zzz").primary["id"] == "a"
    assert playback.pick_version([], None) is None


def test_version_labels():
    vs = playback.group_versions(FILES)
    assert playback.version_label(vs[0], 1, t) == "Director's Cut"
    assert playback.version_label(vs[1], 2, t) == "480p · H.264 · 2 parts"
    assert playback.version_label(vs[2], 3, t) == "1080p · H.264 · HDR10"
    assert playback.version_label(playback.Version([{"id": "x"}]), 4, t) == "Version 4"


def test_plan_follows_the_server_verdict():
    assert playback.plan_from_decision(resp(200, {"decision": "directPlay"})).kind == playback.DIRECT
    assert playback.plan_from_decision(resp(200, {"decision": "directStream"})).kind == playback.REMUX
    p = playback.plan_from_decision(resp(200, {"decision": "transcode", "reason": "remote_bitrate_limit"}))
    assert (p.kind, p.reason) == (playback.TRANSCODE, "remote_bitrate_limit")
    assert playback.plan_from_decision(resp(200, {"decision": "unsupported"})).code == "unsupported"
    assert playback.plan_from_decision(resp(200, {"decision": "damaged"})).kind == playback.UNPLAYABLE


def test_plan_stack_never_direct_plays():
    assert playback.plan_from_decision(resp(200, {"decision": "directPlay"}), stacked=True).kind == playback.REMUX


def test_plan_falls_back_to_direct_play():
    assert playback.plan_from_decision(resp(404, code="NOT_FOUND")).kind == playback.DIRECT
    assert playback.plan_from_decision(api.Response(0)).kind == playback.DIRECT
    assert playback.plan_from_decision(resp(200, {"decision": "somethingNew"})).kind == playback.DIRECT
    assert playback.plan_from_decision(None, stacked=True).kind == playback.REMUX


def test_plan_refusals():
    p = playback.plan_from_decision(resp(429, code="TOO_MANY_SESSIONS", message="Stop one first."))
    assert (p.kind, p.message) == (playback.LIMIT, "Stop one first.")
    p = playback.plan_from_decision(resp(403, code="PARENTAL_LIMIT", message="daily_limit_reached"))
    assert (p.kind, p.code, p.message) == (playback.REFUSED, "PARENTAL_LIMIT", "daily_limit_reached")
    assert playback.plan_from_decision(resp(403, code="PLAYBACK_STOPPED")).code == "PLAYBACK_STOPPED"
    # Another 429 (rate limit) is not a stream limit: play locally.
    assert playback.plan_from_decision(resp(429, code="RATE_LIMITED")).kind == playback.DIRECT


def test_plan_optimized_version_with_fallback():
    p = playback.plan_from_decision(
        resp(
            200,
            {
                "decision": "directPlay",
                "reason": "optimized_version",
                "optimized_version": {"stream_url": "/media/optimized/v1", "stream_token": "st"},
                "source_decision": "transcode",
            },
        )
    )
    assert (p.kind, p.url, p.token) == (playback.OPTIMIZED, "/media/optimized/v1", "st")
    assert p.fallback.kind == playback.TRANSCODE
    p = playback.plan_from_decision(
        resp(
            200,
            {
                "reason": "optimized_version",
                "optimized_version": {"stream_url": "/o"},
                "source_decision": "unsupported",
            },
        )
    )
    assert p.kind == playback.OPTIMIZED and p.fallback is None


def test_transcode_body():
    b = playback.transcode_body("f1", 61234.6, True)
    assert b == {
        "file_id": "f1",
        "height": 0,
        "position_ms": 61234,
        "video_copy": True,
        "audio_stream_index": None,
        "segment_container": "ts",
    }
    b = playback.transcode_body("f1", -5, True, audio_stream_index=1, burn_subtitle_index=4)
    assert b["position_ms"] == 0 and b["audio_stream_index"] == 1
    assert b["burn_subtitle_stream_index"] == 4 and b["video_copy"] is False


def test_urls():
    assert playback.direct_url("http://s", "f 1", "t/k") == "http://s/media/stream/f%201?token=t%2Fk"
    assert playback.with_token("http://s/x?a=1", "") == "http://s/x?a=1"
    assert playback.with_token("http://s/x?a=1", "k") == "http://s/x?a=1&token=k"
    assert playback.absolute("http://s", "/api/v1/transcode/sessions/x/playlist.m3u8?token=t").startswith(
        "http://s/api/"
    )
    assert playback.absolute("http://s", "https://cdn/x") == "https://cdn/x"


def test_session_event_playlist_maps_the_clock():
    s = playback.Session({"session_id": "s", "token": "k", "playlist_url": "/p", "start_offset_sec": 1795.2}, 1800000)
    assert s.offset_ms == 1795200 and not s.full_timeline
    assert s.start_sec == 4.8  # the remux started at the keyframe before the resume point
    assert s.content_ms(10.0) == 1805200


def test_session_full_timeline_starts_at_zero():
    s = playback.Session(
        {"session_id": "s", "playlist_url": "/p", "full_timeline": True, "start_offset_sec": 0}, 1800000
    )
    assert s.offset_ms == 0 and s.start_sec == 1800.0 and s.content_ms(1800.5) == 1800500


def test_session_without_offset_started_where_asked():
    s = playback.Session({"session_id": "s", "playlist_url": "/p"}, 60000)
    assert s.offset_ms == 60000 and s.start_sec == 0.0


def test_durations_and_audio():
    assert playback.item_duration_ms({"duration_ms": 5000}, {"duration_ms": 9}) == 5000
    assert playback.item_duration_ms({}, {"duration_ms": 9}) == 9
    assert playback.item_duration_ms({}, None) == 0
    assert playback.is_audio({"type": "track"})
    assert playback.is_audio({"type": "movie"}, {"video_codec": ""})
    assert not playback.is_audio({"type": "movie"}, {"video_codec": "h264"})


def test_resume_offset():
    assert playback.resume_offset_ms({"view_offset_ms": 5000}, 600000) == 0
    assert playback.resume_offset_ms({"view_offset_ms": 300000}, 600000) == 300000
    assert playback.resume_offset_ms({"view_offset_ms": 590000}, 600000) == 0
    assert playback.resume_offset_ms({"view_offset_ms": 300000}, 0) == 300000


def test_subtitle_jobs():
    f = {
        "id": "f1",
        "stream_token": "st",
        "external_subtitles": [{"id": "x1", "language": "en", "forced": True}, {"language": "fr"}],
        "subtitle_streams": [
            {"index": 3, "language": "fr", "image_based": False},
            {"index": 4, "language": "en", "image_based": True},
            {"index": 5, "language": "es", "image_based": False},
            {"index": 6, "language": "de", "forced": True},
        ],
    }
    direct = playback.subtitle_jobs("http://s", f, False, "es")
    assert direct == [("http://s/media/subtitles/f1/external/x1?token=st", "External 1.en.forced.vtt", "x:x1")]
    session = playback.subtitle_jobs("http://s", f, True, "spa")  # matches "es" streams
    assert [n for _, n, _ in session] == [
        "External 1.en.forced.vtt",
        "Track 4.de.forced.vtt",
        "Track 3.es.vtt",
        "Track 1.fr.vtt",
    ]
    assert session[2][0] == "http://s/media/subtitles/f1/5?token=st" and session[2][2] == "e:5"
    # The track chosen to show comes first, ahead of forced / preferred ones.
    wanted = playback.subtitle_jobs("http://s", f, True, "es", want="e:3")
    assert [k for _, _, k in wanted][:2] == ["x:x1", "e:3"]
