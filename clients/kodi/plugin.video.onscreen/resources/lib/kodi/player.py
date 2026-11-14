"""Resolving a play request (the plugin side of playback).

The plugin turns plugin://...?action=play&id=X into a stream URL for Kodi's
player (setResolvedUrl) and hands what the service needs to it through a
Window(10000) property: the item, the file, the session to delete
afterwards, where the stream's clock starts in the item, and which audio
and subtitle tracks to switch to once the player is up.

Resume. Kodi asks "Resume from / Play from beginning" itself when the list
item had a resume point (listing.py sets one), and tells the plugin the
answer in sys.argv[3] ("resume:true"). The URL says whether the list item
had one (r=1). Without it (a search result, a context-menu play), the
add-on asks the same question with the server's current offset. start=<ms>
in the URL ("Play on…" from another app) starts there without asking. The
start position goes on the resolved item as its resume point, measured on
the stream's clock (a session that already starts at the resume point
needs none).

Tracks. options=1 ("Play with options…") asks for the version, audio and
subtitles; otherwise the account's preferred languages pick them
(onscreen/tracks.py).
"""

import json
import os
import threading
import urllib.request

import xbmc
import xbmcgui
import xbmcplugin
import xbmcvfs

from resources.lib.kodi import common, listitems
from resources.lib.onscreen import api, playback, tracks

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


def burn_refusal_text(resp):
    """The sentence for a session start that refused a burn-in, else ""."""
    if resp is None:
        return ""
    if resp.status == 503 and resp.error_code == "BURN_IN_UNAVAILABLE":
        return common.t("burn_unavailable")
    if resp.status == 400 and resp.error_code == "BURN_IN_UNSUPPORTED":
        return common.t("burn_stacked")
    if resp.status == 400 and resp.error_code in ("INVALID_SUBTITLE_STREAM", "SUBTITLE_NOT_IMAGE"):
        return resp.error_message or common.t("burn_failed")
    return ""


class Choice:
    """The tracks to play. audio: position among the file's audio streams
    (None = the default). subtitle: a tracks.SubtitleChoice, or None to
    leave Kodi's own choice alone."""

    def __init__(self, audio=None, subtitle=None):
        self.audio = audio
        self.subtitle = subtitle

    @property
    def burn_index(self):
        s = self.subtitle
        return s.index if s is not None and s.kind == "embedded" and s.image else None

    @property
    def wanted_key(self):
        s = self.subtitle
        if s is None:
            return ""
        if s.kind == "external":
            return "x:%s" % s.ext_id
        if s.kind == "embedded" and isinstance(s.index, int):
            return "e:%d" % s.index
        return ""


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
    options = params.get("options") == "1" or params.get("choose") == "1"
    version = choose_version(item, params.get("file_id"), options)
    if version is None:
        _fail(handle, common.t("no_files") if not item.get("files") else "")
        return
    f = version.primary
    duration = playback.item_duration_ms(item, f)
    if params.get("start", "").isdigit():
        start_ms = int(params["start"])
    else:
        start_ms = start_position(item, duration, params.get("r") == "1", argv_resume)
    if start_ms is None:
        _fail(handle)  # the person backed out of the resume question
        return

    audio_only = playback.is_audio(item, f) and not version.stacked
    plan = (
        playback.Plan(playback.DIRECT)
        if audio_only
        else playback.plan_from_decision(c.playback_decision(item_id, f["id"]), version.stacked)
    )
    if plan.kind in (playback.LIMIT, playback.REFUSED, playback.UNPLAYABLE):
        common.log("play %s file=%s refused: %s %s" % (item_id, f["id"], plan.kind, plan.code))
        _fail(handle, refusal_text(plan))
        return

    choice = Choice()
    prefs = {}
    if not audio_only:
        try:
            prefs = c.preferences() or {}
        except api.ApiError:
            prefs = {}
        choice = choose_tracks(f, plan, prefs, options)
        if choice is None:
            _fail(handle)  # backed out of a picker
            return
        if choice.burn_index is not None and plan.kind in (playback.REMUX, playback.OPTIMIZED):
            # Burning a picture subtitle in re-encodes the video.
            plan = playback.Plan(playback.TRANSCODE, reason=plan.reason)
        elif plan.kind == playback.OPTIMIZED and choice.audio not in (None, 0):
            # An optimized version carries only the default audio track.
            plan = plan.fallback or playback.Plan(playback.DIRECT)
    common.log(
        "play %s file=%s plan=%s reason=%s start=%dms audio=%s subtitle=%s"
        % (item_id, f["id"], plan.kind, plan.reason, start_ms, choice.audio, choice.subtitle)
    )

    stream = build_stream(c, item_id, f, plan, start_ms, choice)
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
    elif audio_only:
        li.setContentLookup(False)

    select = {"audio": None, "subtitle": None, "subtitles_off": False}
    if not audio_only:
        subs, keys = subtitles(c, f, stream, prefs.get("preferred_subtitle_lang") or "", choice.wanted_key)
        if subs:
            li.setSubtitles(subs)
        select = selection(f, stream, choice, keys)

    item_info = {k: item.get(k) for k in ("type", "title", "index", "index_end", "parent_id", "grandparent_id")}
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
                "select": select,
                "item": item_info,
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


def choose_tracks(f, plan, prefs, ask):
    """The Choice for this playback, or None when the person backed out of
    a picker. Without ask, the account's preferred audio language picks the
    audio track, and its preferred subtitle language (or the forced-only
    rule) a text subtitle; nothing is burned in unasked."""
    # An optimized version, like a session, carries none of the file's
    # subtitle streams: text ones come as files, picture ones burn in.
    session = plan.needs_session or plan.kind == playback.OPTIMIZED
    audio = tracks.preferred_audio(f, prefs.get("preferred_audio_lang"))
    pref_sub = prefs.get("preferred_subtitle_lang") or ""
    forced_only = bool(prefs.get("forced_subtitles_only"))
    choices = tracks.subtitle_choices(f, common.t, session, common.language_name)
    if not ask:
        sub = None
        if pref_sub or forced_only or session:
            # Text before picture tracks (Kodi draws PGS in a direct play,
            # but text is the better default); a session can't use
            # pictures without burning them in.
            candidates = sorted((c for c in choices[1:] if not (session and c.image)), key=lambda c: c.image)
            rows = [{"language": c.language, "forced": c.forced, "default": c.default, "_c": c} for c in candidates]
            picked = tracks.preferred_subtitle(rows, pref_sub, forced_only)
            sub = picked["_c"] if picked else None
        return Choice(audio, sub)
    labels = tracks.audio_labels(f, common.t, common.language_name)
    if len(labels) > 1:
        i = xbmcgui.Dialog().select(common.t("choose_audio"), labels, preselect=audio if audio is not None else 0)
        if i < 0:
            return None
        audio = i
    if len(choices) > 1:
        i = xbmcgui.Dialog().select(common.t("choose_subtitles"), [c.label for c in choices])
        if i < 0:
            return None
        return Choice(audio, choices[i])
    return Choice(audio, None)


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


def ensure_adaptive():
    """InputStream Adaptive, which plays a growing transcode playlist with
    seeking (Kodi's own HLS player treats it as live and can't seek): True
    when it's there. Enables it when installed but off; otherwise offers
    Kodi's install once (Settings > Advanced turns the offer back on)."""
    if adaptive_available():
        return True
    if xbmc.getCondVisibility("System.HasAddon(inputstream.adaptive)"):
        xbmc.executebuiltin("EnableAddon(inputstream.adaptive)", True)
    elif common.setting_bool("offer_adaptive"):
        common.addon().setSettingBool("offer_adaptive", False)
        common.notify(common.t("adaptive_why"), ms=8000)
        xbmc.executebuiltin("InstallAddon(inputstream.adaptive)", True)
    else:
        return False
    monitor = xbmc.Monitor()
    for _ in range(40):
        if adaptive_available():
            return True
        if monitor.waitForAbort(0.5):
            break
    return adaptive_available()


def build_stream(c, item_id, f, plan, start_ms, choice=None):
    """The stream for a plan: dict(url, hls, adaptive, full_timeline,
    start_sec, offset_ms, decision, session), a refusal Plan, or None."""
    choice = choice or Choice()
    server = c.server_url
    if plan.kind == playback.OPTIMIZED:
        token = plan.token or c.store.load().get("asset_token", "")
        return {
            "url": playback.with_token(playback.absolute(server, plan.url), token),
            "hls": False,
            "optimized": True,
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
    body = playback.transcode_body(
        f["id"],
        start_ms,
        plan.kind == playback.REMUX,
        audio_stream_index=choice.audio,
        burn_subtitle_index=choice.burn_index,
    )
    resp = c.transcode_start(item_id, body)
    refused_burn = burn_refusal_text(resp) if choice.burn_index is not None else ""
    if refused_burn:
        # Say why, and play without the burned-in subtitle.
        xbmcgui.Dialog().ok("OnScreen", refused_burn)
        body = playback.transcode_body(f["id"], start_ms, plan.kind == playback.REMUX, audio_stream_index=choice.audio)
        choice.subtitle = None
        resp = c.transcode_start(item_id, body)
    refused = playback.refusal(resp)
    if refused:
        return refused
    if not resp.ok or not isinstance(resp.data, dict) or not resp.data.get("playlist_url"):
        common.log("transcode start failed: %s %s" % (resp.status, resp.error_message), xbmc.LOGWARNING)
        return None
    sess = playback.Session(resp.data, start_ms)
    adaptive = adaptive_available() or (not sess.full_timeline and ensure_adaptive())
    return {
        "url": playback.absolute(server, sess.playlist_url),
        "hls": True,
        "adaptive": adaptive,
        "full_timeline": sess.full_timeline,
        "start_sec": sess.start_sec,
        "offset_ms": sess.offset_ms,
        "decision": "directStream" if plan.kind == playback.REMUX else "transcode",
        "session": {"id": sess.id, "token": sess.token},
        "burned": choice.burn_index is not None,
    }


def selection(f, stream, choice, downloaded_keys):
    """What the service switches to once Kodi plays: positions in Kodi's
    audio and subtitle lists. A direct play lists the file's own subtitle
    streams first, then the downloaded files; a session lists only the
    downloaded files (its audio is the one track the session carries)."""
    out = {"audio": None, "subtitle": None, "subtitles_off": False}
    session = bool(stream.get("hls") or stream.get("optimized"))
    if choice.audio is not None and not session:
        out["audio"] = choice.audio
    s = choice.subtitle
    if s is None or stream.get("burned"):
        return out
    if s.kind == "off":
        out["subtitles_off"] = True
        return out
    embedded = 0 if session else len([x for x in (f.get("subtitle_streams") or []) if isinstance(x, dict)])
    key = choice.wanted_key
    if not session and s.kind == "embedded":
        out["subtitle"] = tracks.embedded_subtitle_position(f, s.index)
    elif key in downloaded_keys:
        out["subtitle"] = embedded + downloaded_keys.index(key)
    return out


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


def subtitles(c, f, stream, preferred_lang, want=""):
    """Download the subtitle files for this playback (in parallel, each
    bounded by SUB_TIMEOUT). Returns (local paths, their keys), in the
    order Kodi will list them."""
    jobs = playback.subtitle_jobs(
        c.server_url, f, bool(stream.get("hls") or stream.get("optimized")), preferred_lang, want
    )
    if not jobs:
        return [], []
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

    threads = [threading.Thread(target=fetch, args=(i, u, n)) for i, (u, n, _k) in enumerate(jobs)]
    for th in threads:
        th.start()
    for th in threads:
        th.join(SUB_TIMEOUT + 2)
    done = [(p, jobs[i][2]) for i, p in enumerate(paths) if p]
    return [p for p, _ in done], [k for _, k in done]
