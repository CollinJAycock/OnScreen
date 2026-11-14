"""Resolving a play request (the plugin side of playback).

The plugin turns plugin://...?action=play&id=X into a stream URL for Kodi's
player (setResolvedUrl) and hands what the service needs to report progress
to it through a Window(10000) property: the item, the file, the session to
delete afterwards and where the stream's clock starts in the item.

Resume. Kodi asks "Resume from / Play from beginning" itself when the list
item had a resume point (listing.py sets one), and tells the plugin the
answer in sys.argv[3] ("resume:true"). The URL says whether the list item
had one (r=1). Without it (a search result, a context-menu play), the
add-on asks the same question with the server's current offset. The start
position goes on the resolved item as its resume point, measured on the
stream's clock (a session that already starts at the resume point needs
none).
"""

import json
import os
import threading
import urllib.parse
import urllib.request

import xbmc
import xbmcgui
import xbmcplugin
import xbmcvfs

from resources.lib.kodi import common, listitems
from resources.lib.onscreen import api, playback

SUB_TIMEOUT = 20


def _fmt_time(ms):
    s = int(ms // 1000)
    h, rem = divmod(s, 3600)
    m, s = divmod(rem, 60)
    return "%d:%02d:%02d" % (h, m, s) if h else "%d:%02d" % (m, s)


def _fail(handle, message=""):
    if message:
        xbmcgui.Dialog().ok("OnScreen", message)
    if handle >= 0:
        xbmcplugin.setResolvedUrl(handle, False, xbmcgui.ListItem())


def refusal_text(plan):
    """The sentence for a LIMIT / REFUSED / UNPLAYABLE plan. The server
    words the too-many-streams and admin-stop sentences itself (in Kodi's
    language); a parental limit's message is a reason code."""
    if plan.kind == playback.LIMIT:
        return plan.message or common.t("too_many_streams_text")
    if plan.code == "PLAYBACK_STOPPED":
        return plan.message or common.t("admin_stopped_plain")
    if plan.code == "PARENTAL_LIMIT":
        if plan.message == "outside_allowed_hours":
            return common.t("parental_hours")
        if plan.message == "daily_limit_reached":
            return common.t("parental_daily")
        return common.t("parental_blocked")
    if plan.code == "unsupported":
        return common.t("dv_unsupported")
    if plan.code == "damaged":
        return common.t("damaged")
    return common.t("playback_failed")


def resolve(handle, params, argv_resume):
    """Resolve params["id"] for Kodi's player. argv_resume is sys.argv[3]
    ("resume:true", "resume:false" or "")."""
    c = common.client()
    if not c.signed_in():
        _fail(handle, common.t("session_expired"))
        return
    item_id = params.get("id", "")
    try:
        item = c.item(item_id)
    except api.ApiError as e:
        _fail(handle, e.message or common.t("playback_failed"))
        return
    version = choose_version(item, params.get("file_id"), params.get("choose") == "1")
    if version is None:
        _fail(handle, common.t("no_files") if not item.get("files") else "")
        return
    f = version.primary
    duration = playback.item_duration_ms(item, f)
    start_ms = start_position(item, duration, params.get("r") == "1", argv_resume)
    if start_ms is None:
        _fail(handle)  # the person backed out of the resume question
        return

    audio = playback.is_audio(item, f) and not version.stacked
    plan = (
        playback.Plan(playback.DIRECT)
        if audio
        else playback.plan_from_decision(c.playback_decision(item_id, f["id"]), version.stacked)
    )
    common.log("play %s file=%s plan=%s reason=%s start=%dms" % (item_id, f["id"], plan.kind, plan.reason, start_ms))
    if plan.kind in (playback.LIMIT, playback.REFUSED, playback.UNPLAYABLE):
        _fail(handle, refusal_text(plan))
        return

    stream = build_stream(c, item_id, f, plan, start_ms)
    if stream is None:
        return _fail(handle, common.t("playback_failed"))
    if isinstance(stream, playback.Plan):  # a refusal at session start
        return _fail(handle, refusal_text(stream))

    li = xbmcgui.ListItem(path=stream["url"], offscreen=True)
    e = _entry_for(item)
    if e is not None:
        if e.music:
            listitems.music_tag(li, e.info, e.mediatype)
        else:
            # A fresh tag on the resolved item replaces the list item's
            # (and its resume point); Kodi starts at this one's.
            tag = listitems.video_tag(li, e.info, e.mediatype)
            if stream["start_sec"] > 0:
                listitems._call(tag, "setResumePoint", stream["start_sec"], duration / 1000.0)
        art = listitems.ArtResolver(c.server_url, c.store.load().get("asset_token", ""))
        images = {k: art.url(p, w) for k, (p, w) in e.art.items()}
        li.setArt({k: v for k, v in images.items() if v})
    if stream["hls"]:
        li.setMimeType("application/vnd.apple.mpegurl")
        li.setContentLookup(False)
        if stream["adaptive"]:
            li.setProperty("inputstream", "inputstream.adaptive")
            if common.kodi_major() < 21:
                li.setProperty("inputstream.adaptive.manifest_type", "hls")
            if not stream["full_timeline"]:
                # A growing (EVENT) playlist: start from its first segment,
                # not the live edge.
                li.setProperty("inputstream.adaptive.play_timeshift_buffer", "true")
    elif audio:
        li.setContentLookup(False)
    subs = subtitles(c, item, f, stream)
    if subs:
        li.setSubtitles(subs)

    xbmcgui.Window(10000).setProperty(
        common.PROP_PLAYBACK,
        json.dumps(
            {
                "item_id": item_id,
                "file_id": f["id"],
                "url": stream["url"],
                "duration_ms": duration,
                "offset_ms": stream["offset_ms"],
                "decision": stream["decision"],
                "session": stream.get("session"),
                "full_timeline": stream["full_timeline"],
            }
        ),
    )
    xbmcplugin.setResolvedUrl(handle, True, li)


def _entry_for(item):
    from resources.lib.onscreen import listing

    return listing.entry(item, common.t)


def choose_version(item, file_id, ask):
    versions = playback.group_versions(item.get("files"))
    if not versions:
        return None
    if file_id:
        return playback.pick_version(item.get("files"), file_id)
    if len(versions) > 1 and (ask or common.setting_bool("ask_version")):
        labels = [playback.version_label(v, i + 1, common.t) for i, v in enumerate(versions)]
        i = xbmcgui.Dialog().select(common.t("choose_version"), labels)
        if i < 0:
            return None
        return versions[i]
    return versions[0]


def start_position(item, duration, listed_resume, argv_resume):
    """Where to start in ms, or None when the person cancelled."""
    offset = playback.resume_offset_ms(item, duration)
    if not offset:
        return 0
    if argv_resume == "resume:true":
        return offset
    if listed_resume and argv_resume == "resume:false":
        return 0  # Kodi asked; "from the beginning"
    i = xbmcgui.Dialog().contextmenu([common.t("resume_from", time=_fmt_time(offset)), common.t("play_beginning")])
    if i < 0:
        return None
    return offset if i == 0 else 0


def adaptive_available():
    return xbmc.getCondVisibility("System.AddonIsEnabled(inputstream.adaptive)")


def build_stream(c, item_id, f, plan, start_ms):
    """The stream for a plan: dict(url, hls, adaptive, full_timeline,
    start_sec, offset_ms, decision, session), a refusal Plan, or None."""
    server = c.server_url
    if plan.kind == playback.OPTIMIZED:
        token = plan.token or c.store.load().get("asset_token", "")
        return {
            "url": playback.with_token(playback.absolute(server, plan.url), token),
            "hls": False,
            "adaptive": False,
            "full_timeline": True,
            "start_sec": start_ms / 1000.0,
            "offset_ms": 0,
            "decision": "directPlay",
        }
    if plan.kind == playback.DIRECT:
        token = f.get("stream_token") or c.store.load().get("asset_token", "")
        return {
            "url": playback.direct_url(server, f["id"], token),
            "hls": False,
            "adaptive": False,
            "full_timeline": True,
            "start_sec": start_ms / 1000.0,
            "offset_ms": 0,
            "decision": "directPlay",
        }
    body = playback.transcode_body(f["id"], start_ms, plan.kind == playback.REMUX)
    resp = c.transcode_start(item_id, body)
    refused = playback.refusal(resp)
    if refused:
        return refused
    if not resp.ok or not isinstance(resp.data, dict) or not resp.data.get("playlist_url"):
        common.log("transcode start failed: %s %s" % (resp.status, resp.error_message), xbmc.LOGWARNING)
        return None
    sess = playback.Session(resp.data, start_ms)
    return {
        "url": playback.absolute(server, sess.playlist_url),
        "hls": True,
        "adaptive": adaptive_available(),
        "full_timeline": sess.full_timeline,
        "start_sec": sess.start_sec,
        "offset_ms": sess.offset_ms,
        "decision": "directStream" if plan.kind == playback.REMUX else "transcode",
        "session": {"id": sess.id, "token": sess.token},
    }


# ── Subtitles ───────────────────────────────────────────────────────────────


def _sub_dir():
    path = xbmcvfs.translatePath("special://temp/onscreen-subs/")
    os.makedirs(path, exist_ok=True)
    for name in os.listdir(path):  # last playback's
        try:
            os.remove(os.path.join(path, name))
        except OSError:
            pass
    return path


def subtitles(c, item, f, stream):
    """Download the subtitle files for this playback (in parallel, each
    bounded by SUB_TIMEOUT) and return their local paths."""
    pref = ""
    if stream.get("hls"):
        try:
            pref = (c.preferences() or {}).get("preferred_subtitle_lang") or ""
        except api.ApiError:
            pref = ""
    jobs = playback.subtitle_jobs(c.server_url, f, stream.get("hls"), pref)
    if not jobs:
        return []
    folder = _sub_dir()
    paths = [None] * len(jobs)
    ua = c.headers.get("User-Agent", "OnScreen-Kodi")

    def fetch(i, url, name):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": ua})
            with urllib.request.urlopen(req, timeout=SUB_TIMEOUT) as r:
                data = r.read()
            if data:
                p = os.path.join(folder, name)
                with open(p, "wb") as out:
                    out.write(data)
                paths[i] = p
        except Exception as e:  # noqa: BLE001 - a missing subtitle mustn't stop playback
            common.log("subtitle %s: %s" % (name, e), xbmc.LOGWARNING)

    threads = [threading.Thread(target=fetch, args=(i, u, n)) for i, (u, n) in enumerate(jobs)]
    for th in threads:
        th.start()
    for th in threads:
        th.join(SUB_TIMEOUT + 2)
    return [p for p in paths if p]
