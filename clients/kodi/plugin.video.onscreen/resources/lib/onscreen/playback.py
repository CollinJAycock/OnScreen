"""How an item plays: which file, which path (direct, remux, transcode,
optimized version) and the URLs. Pure, so it's tested without Kodi.

The server's play decision is final (POST /items/{id}/playback-decision): it
judged the file against the X-Client-Capabilities header plus what the
device can't know (the viewer's quality profile, the internet bitrate cap,
the integrity probe). Only when that call fails (no answer, a server that
predates it) does the add-on decide alone, and Kodi plays nearly every file
directly, so the fallback is a direct play. Mirrors the Roku channel's
source/playback/Decide.brs and the TV apps.
"""

import re
import urllib.parse

from resources.lib.onscreen.tracks import lang_matches

AUDIO_TYPES = frozenset(("track", "audiobook", "audiobook_chapter", "podcast_episode"))

# Plan kinds.
DIRECT = "direct"
REMUX = "remux"
TRANSCODE = "transcode"
OPTIMIZED = "optimized"
LIMIT = "limit"  # 429 TOO_MANY_SESSIONS: show the message, don't play
REFUSED = "refused"  # 403 PLAYBACK_STOPPED / PARENTAL_LIMIT
UNPLAYABLE = "unplayable"  # unsupported (Dolby Vision 5 nobody can tone-map) / damaged


class Version:
    """A file the viewer can pick, or the parts of a multi-part movie
    (files[].part_index), which play together as one version."""

    def __init__(self, files):
        self.files = files
        self.primary = files[0]

    @property
    def stacked(self):
        return len(self.files) > 1


def _str(v):
    return v if isinstance(v, str) else ""


def _stack_key(f):
    if not isinstance(f, dict) or f.get("part_index") is None:
        return ""
    return "part|%s|%s" % (_str(f.get("edition")).strip().lower(), _str(f.get("container")).lower())


def group_versions(files):
    """files grouped into versions, in the server's order (best first). The
    server sets part_index only on files that really play as a stack, and a
    session started with part 1's file_id streams every part, so a part is
    never offered on its own. Mirrors Versions_Group (Roku) and
    web/src/lib/fileVersions.ts."""
    out = []
    if not isinstance(files, list):
        return out
    seen = set()
    for f in files:
        if not isinstance(f, dict) or not _str(f.get("id")) or f["id"] in seen:
            continue
        key = _stack_key(f)
        parts = [f]
        if key:
            same = [g for g in files if isinstance(g, dict) and _stack_key(g) == key]
            if len(same) >= 2:
                parts = sorted(same, key=lambda g: g.get("part_index") or 0)
        for p in parts:
            seen.add(p.get("id"))
        out.append(Version(parts))
    return out


def pick_version(files, file_id=None):
    """The version holding file_id (a part names its whole stack), else the
    first; None when there are no files."""
    versions = group_versions(files)
    if not versions:
        return None
    if file_id:
        for v in versions:
            if any(p.get("id") == file_id for p in v.files):
                return v
    return versions[0]


def tech_label(f):
    """ "4K · HEVC", "1080p · H.264", or "" when the file says neither."""
    parts = []
    h = f.get("resolution_h") or 0
    if isinstance(h, int) and h >= 2000:
        parts.append("4K")
    elif isinstance(h, int) and h > 0:
        parts.append("%dp" % h)
    codec = _str(f.get("video_codec")).lower()
    if codec in ("h264", "avc"):
        parts.append("H.264")
    elif codec in ("hevc", "h265"):
        parts.append("HEVC")
    elif codec:
        parts.append(codec.upper())
    hdr = _str(f.get("hdr_type")).lower()
    if hdr == "dolby_vision":
        parts.append("Dolby Vision")
    elif hdr in ("hdr10", "hdr10plus"):
        parts.append("HDR10+" if hdr == "hdr10plus" else "HDR10")
    elif hdr == "hlg":
        parts.append("HLG")
    return " · ".join(parts)


def version_label(v, n, t):
    """Picker label: the edition the file name gives, else the technical
    description, else "Version n"; plus the part count. t(key, **params) is
    the string lookup."""
    base = _str(v.primary.get("edition")).strip() or tech_label(v.primary) or t("version_n", n=n)
    if v.stacked:
        base = t("version_parts", version=base, count=len(v.files))
    return base


class Plan:
    """What to do with the chosen file. kind is one of the constants above.
    For OPTIMIZED: url + token of the version and fallback, the source's own
    plan for when the version won't play. For LIMIT / REFUSED / UNPLAYABLE:
    message (the server's sentence; may be empty, the caller then words it)
    and code."""

    def __init__(self, kind, message="", code="", url="", token="", fallback=None, reason=""):
        self.kind = kind
        self.message = message
        self.code = code
        self.url = url
        self.token = token
        self.fallback = fallback
        self.reason = reason

    @property
    def needs_session(self):
        return self.kind in (REMUX, TRANSCODE)

    def __repr__(self):
        return "Plan(%s%s)" % (self.kind, ", " + self.code if self.code else "")


_VERDICTS = {"directPlay": DIRECT, "directStream": REMUX, "transcode": TRANSCODE}


def refusal(resp):
    """A Plan for a response that refuses playback outright, else None:
    429 TOO_MANY_SESSIONS, 403 PLAYBACK_STOPPED (an admin stopped this
    stream) or 403 PARENTAL_LIMIT (the message is a reason code)."""
    if resp is None:
        return None
    if resp.status == 429 and resp.error_code == "TOO_MANY_SESSIONS":
        return Plan(LIMIT, resp.error_message, resp.error_code)
    if resp.status == 403 and resp.error_code in ("PLAYBACK_STOPPED", "PARENTAL_LIMIT"):
        return Plan(REFUSED, resp.error_message, resp.error_code)
    return None


def plan_from_decision(resp, stacked=False):
    """The Plan for a playback-decision Response. A failed call or a verdict
    this build doesn't know plays directly (remux for a stack: one file is
    only one part)."""
    refused = refusal(resp)
    if refused:
        return refused
    local = Plan(REMUX if stacked else DIRECT)
    if resp is None or not resp.ok or not isinstance(resp.data, dict):
        return local
    d = resp.data
    reason = _str(d.get("reason"))
    ov = d.get("optimized_version")
    if reason == "optimized_version" and isinstance(ov, dict) and _str(ov.get("stream_url")):
        fallback = _verdict_plan(d.get("source_decision"), stacked) or local
        if fallback.kind == UNPLAYABLE:
            fallback = None
        return Plan(
            OPTIMIZED, url=ov["stream_url"], token=_str(ov.get("stream_token")), fallback=fallback, reason=reason
        )
    plan = _verdict_plan(d.get("decision"), stacked)
    if plan is None:
        return local
    plan.reason = reason
    return plan


def _verdict_plan(verdict, stacked):
    v = _str(verdict)
    if v in ("unsupported", "damaged"):
        return Plan(UNPLAYABLE, code=v)
    kind = _VERDICTS.get(v)
    if kind is None:
        return None
    if kind == DIRECT and stacked:
        kind = REMUX
    return Plan(kind)


def transcode_body(file_id, position_ms, video_copy, audio_stream_index=None, burn_subtitle_index=None):
    """The POST /items/{id}/transcode body. segment_container "ts": AC-3 /
    E-AC-3 copies and HEVC ride MPEG-TS, which every Kodi HLS path demuxes;
    the server keeps fMP4 where TS can't carry the stream (AV1). height 0
    lets the server size the output (and use its adaptive ladder when it
    has one switched on). A subtitle burn-in is always a full re-encode."""
    body = {
        "file_id": file_id,
        "height": 0,
        "position_ms": max(0, int(position_ms or 0)),
        "video_copy": bool(video_copy),
        "audio_stream_index": audio_stream_index
        if isinstance(audio_stream_index, int) and audio_stream_index >= 0
        else None,
        "segment_container": "ts",
    }
    if isinstance(burn_subtitle_index, int) and burn_subtitle_index >= 0:
        body["burn_subtitle_stream_index"] = burn_subtitle_index
        body["video_copy"] = False
    return body


def with_token(url, token):
    """url with ?token= (or &token=) appended; unchanged without a token."""
    if not token:
        return url
    return url + ("&" if "?" in url else "?") + "token=" + urllib.parse.quote(token, safe="")


def direct_url(server_url, file_id, stream_token):
    return with_token(server_url + "/media/stream/" + urllib.parse.quote(file_id, safe=""), stream_token)


def absolute(server_url, path):
    """A server-relative URL (the session's playlist_url, an optimized
    version's stream_url) made absolute."""
    if path.startswith("http://") or path.startswith("https://"):
        return path
    if not path.startswith("/"):
        path = "/" + path
    return server_url + path


class Session:
    """A remux / transcode session as the player tracks it.

    offset_ms is the content position the stream's clock starts at:
    start_offset_sec when the session started mid-item (the keyframe before
    the requested position on a remux), 0 for a full-timeline playlist (a
    VOD remux or an adaptive ladder), where every position is in the
    playlist from the start and the player seeks to the resume point
    itself."""

    def __init__(self, data, requested_ms):
        self.id = _str(data.get("session_id"))
        self.token = _str(data.get("token"))
        self.playlist_url = _str(data.get("playlist_url"))
        self.full_timeline = data.get("full_timeline") is True
        self.segment_format = _str(data.get("segment_format"))
        start = data.get("start_offset_sec")
        if self.full_timeline:
            self.offset_ms = 0
        elif isinstance(start, (int, float)) and not isinstance(start, bool) and start >= 0:
            self.offset_ms = int(round(start * 1000))
        else:
            self.offset_ms = max(0, int(requested_ms or 0))
        # Where the player should start inside the stream, in seconds.
        self.start_sec = max(0.0, (max(0, int(requested_ms or 0)) - self.offset_ms) / 1000.0)
        if self.start_sec < 1.0:
            self.start_sec = 0.0

    def content_ms(self, player_sec):
        """Content position for the player's clock (seconds into the stream)."""
        return self.offset_ms + int(round(max(0.0, player_sec) * 1000))


def item_duration_ms(item, file=None):
    """The item's length: its own duration_ms, else its file's. A session's
    stream is no measure: a growing playlist reports what's written so far,
    and progress against it would pass the server's 90 % watched mark
    early."""
    for src in (item, file):
        if isinstance(src, dict):
            d = src.get("duration_ms")
            if isinstance(d, int) and d > 0:
                return d
    return 0


def is_audio(item, file=None):
    """An audio-only item: a track, book or podcast type, or a file with no
    video stream. Audio always direct-plays (Kodi's audio player decodes
    every format the server indexes, DSD included)."""
    if _str(item.get("type")) in AUDIO_TYPES:
        return True
    if isinstance(file, dict) and not _str(file.get("video_codec")):
        return True
    return False


def resume_offset_ms(item, duration_ms):
    """The resume point worth offering: the saved offset unless it's under
    10 s in or within the last 5 % (or 30 s), where starting over is what
    anyone wants."""
    pos = item.get("view_offset_ms")
    if not isinstance(pos, int) or pos < 10000:
        return 0
    if duration_ms > 0 and (pos >= duration_ms * 0.95 or duration_ms - pos < 30000):
        return 0
    return pos


# ── Subtitles ───────────────────────────────────────────────────────────────


def _safe(s):
    return re.sub(r"[^A-Za-z0-9._-]+", "_", s or "")[:40] or "sub"


def subtitle_jobs(server, f, session, preferred_lang, want=""):
    """[(url, filename, key)] to download; key is "x:<id>" for an external
    subtitle, "e:<stream index>" for an embedded one. External subtitles
    always (Kodi can't see them in the file). Embedded text tracks only for
    a session (session=True): the server's HLS output carries no subtitles,
    while a direct play has them all. Picture subtitles (PGS, VobSub) can't
    become text (a session burns them in instead). want names a track that
    must be among them (the one chosen to show)."""
    token = f.get("stream_token") or ""
    fid = urllib.parse.quote(f["id"], safe="")
    jobs = []
    for i, ext in enumerate(f.get("external_subtitles") or []):
        if not isinstance(ext, dict) or not ext.get("id"):
            continue
        url = server + "/media/subtitles/%s/external/%s" % (fid, urllib.parse.quote(ext["id"], safe=""))
        # Kodi lists a subtitle file by its name ("External 1 (External)")
        # and reads the language and forced flag from the suffixes.
        name = "External %d.%s%s.vtt" % (
            i + 1,
            _safe(ext.get("language") or "und"),
            ".forced" if ext.get("forced") else "",
        )
        jobs.append((with_token(url, token), name, "x:%s" % ext["id"]))
    if session:
        position = {
            s.get("index"): n
            for n, s in enumerate((x for x in (f.get("subtitle_streams") or []) if isinstance(x, dict)), 1)
        }
        text = [
            s
            for s in (f.get("subtitle_streams") or [])
            if isinstance(s, dict) and not s.get("image_based") and isinstance(s.get("index"), int)
        ]
        # The wanted track, then forced and preferred-language ones; at most
        # four, since each is cut out of the whole file on the server.
        text.sort(
            key=lambda s: (
                0 if "e:%d" % s["index"] == want else 1,
                0 if s.get("forced") else 1,
                0 if lang_matches(s.get("language"), preferred_lang) else 1,
            )
        )
        for s in text[:4]:
            url = server + "/media/subtitles/%s/%d" % (fid, s["index"])
            name = "Track %d.%s%s.vtt" % (
                position.get(s["index"], s["index"]),
                _safe(s.get("language") or "und"),
                ".forced" if s.get("forced") else "",
            )
            jobs.append((with_token(url, token), name, "e:%d" % s["index"]))
    return jobs
