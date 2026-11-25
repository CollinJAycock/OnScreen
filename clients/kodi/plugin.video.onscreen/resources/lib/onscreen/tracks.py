"""Audio and subtitle choices, and the preferred-language picks.

Language matching is the web app's (web/src/routes/watch/[id]/
subtitle-select.ts): ffprobe tags tracks with ISO 639-2 codes ("eng") while
the account's preferences are ISO 639-1 ("en"), and region subtags ("pt-BR")
match on the language.

Where a choice lands depends on the stream:
- Direct play: Kodi demuxes every track itself, so a choice is applied on
  Kodi's player once it starts (the service calls setAudioStream /
  setSubtitleStream with the track's position among its kind).
- Remux / transcode: the session carries one audio track, so the audio
  choice goes into the session request (audio_stream_index); text
  subtitles come as downloaded files; a picture subtitle (PGS, VobSub) can
  only be burned into the picture, which makes the session a full transcode.
"""

ISO6392_TO_1 = {
    "eng": "en",
    "spa": "es",
    "fre": "fr",
    "fra": "fr",
    "ger": "de",
    "deu": "de",
    "ita": "it",
    "por": "pt",
    "rus": "ru",
    "jpn": "ja",
    "chi": "zh",
    "zho": "zh",
    "kor": "ko",
    "ara": "ar",
    "dut": "nl",
    "nld": "nl",
    "swe": "sv",
    "nor": "no",
    "dan": "da",
    "fin": "fi",
    "pol": "pl",
    "tur": "tr",
    "heb": "he",
    "hin": "hi",
    "tha": "th",
    "vie": "vi",
    "ces": "cs",
    "cze": "cs",
    "gre": "el",
    "ell": "el",
    "hun": "hu",
    "ron": "ro",
    "rum": "ro",
    "ukr": "uk",
    "ind": "id",
}


def normalize_lang(code):
    """ "ENG" -> "en", "en-US" -> "en", "pt_BR" -> "pt", "xyz" -> "xyz"."""
    if not isinstance(code, str) or not code:
        return ""
    primary = code.lower().replace("_", "-").split("-")[0]
    return ISO6392_TO_1.get(primary, primary)


def lang_matches(a, b):
    na = normalize_lang(a)
    return na != "" and na == normalize_lang(b)


def _streams(f, key):
    out = f.get(key) if isinstance(f, dict) else None
    return [s for s in out if isinstance(s, dict)] if isinstance(out, list) else []


def preferred_audio(f, preferred_lang):
    """Position (among the file's audio streams) of the first track in the
    preferred language, or None for the file's default."""
    if not preferred_lang:
        return None
    for i, s in enumerate(_streams(f, "audio_streams")):
        if lang_matches(s.get("language"), preferred_lang):
            return i
    return None


def default_audio(f):
    """Position of the audio track the file marks default, else 0."""
    for i, s in enumerate(_streams(f, "audio_streams")):
        if s.get("default"):
            return i
    return 0


def playing_audio_language(f, audio, session):
    """The language of the audio track that plays: the chosen one (a
    position), else a session's first track (the server maps 0:a:0) or, in
    a direct play, the one the file marks default (Kodi's own pick under its
    default "original language" setting)."""
    streams = _streams(f, "audio_streams")
    if audio is None:
        audio = 0 if session else default_audio(f)
    if 0 <= audio < len(streams):
        lang = streams[audio].get("language")
        return lang if isinstance(lang, str) else ""
    return ""


# Show subtitles (users.subtitle_mode, server migration 00066;
# docs/foreign-audio-subtitles.md): when the preferred subtitle language
# comes on by itself.
#   always  - the full track in it (plain before SDH), else a forced one
#   forced  - a forced track in it, else none
#   foreign - as always while the audio is in a known other language, else
#             as forced
MODES = ("always", "forced", "foreign")

# Tags that name no single language: an untagged or mixed audio track isn't
# "foreign" (most untagged files are in the viewer's own language).
NO_LANGUAGE = {"", "und", "mul", "zxx", "mis"}


def subtitle_mode_of(prefs):
    """The mode a preferences answer means: subtitle_mode, or from a server
    before it forced_subtitles_only (on = forced, off = always)."""
    prefs = prefs if isinstance(prefs, dict) else {}
    mode = prefs.get("subtitle_mode")
    if mode in MODES:
        return mode
    return "forced" if prefs.get("forced_subtitles_only") is True else "always"


def is_foreign_audio(audio_lang, subtitle_lang):
    """The audio is in a known language other than the subtitle language."""
    a = normalize_lang(audio_lang)
    return a not in NO_LANGUAGE and a != normalize_lang(subtitle_lang)


def preferred_subtitle(subs, preferred_lang, mode, audio_lang=None):
    """The subtitle to turn on, or None for off. Mirrors the web's
    pickPreferredSubtitle: among the tracks in the preferred language, the
    full one (plain before SDH, else a forced one) under "always" or under
    "foreign" when audio_lang is foreign; otherwise a forced one. With no
    preference, the track the file marks default (under forced and foreign
    only when it's forced)."""
    subs = [s for s in subs if isinstance(s, dict)]
    if not preferred_lang:
        default = next((s for s in subs if s.get("default")), None)
        if default is None or (mode != "always" and not default.get("forced")):
            return None
        return default
    in_lang = [s for s in subs if lang_matches(s.get("language"), preferred_lang)]
    forced = next((s for s in in_lang if s.get("forced")), None)
    full = next((s for s in in_lang if not s.get("forced") and not s.get("sdh")), None) or next(
        (s for s in in_lang if not s.get("forced")), None
    )
    want_full = mode == "always" or (mode == "foreign" and is_foreign_audio(audio_lang, preferred_lang))
    return (full or forced) if want_full else forced


def _label(s, n, t, lang_name=None):
    """ "English · Commentary": the language (named by lang_name when given,
    Kodi's convertLanguage in the add-on), then the title unless it only
    repeats the language; "Track n" with neither."""
    parts = []
    title = s.get("title") if isinstance(s.get("title"), str) else ""
    lang = s.get("language") if isinstance(s.get("language"), str) else ""
    if lang:
        parts.append((lang_name(lang) if lang_name else "") or lang)
    if title and title.lower() not in (lang.lower(), parts[0].lower() if parts else ""):
        parts.append(title)
    if not parts:
        parts.append(t("track_n", n=n))
    return " · ".join(parts)


def _audio_format(s):
    """ffprobe's profile when it says more than the codec ("Dolby TrueHD +
    Dolby Atmos", "DTS-HD MA"), else the codec ("AAC", not AAC's "LC")."""
    profile = s.get("profile") if isinstance(s.get("profile"), str) else ""
    codec = s.get("codec") if isinstance(s.get("codec"), str) else ""
    if len(profile.strip()) > 4:
        return profile.strip()
    return codec.upper()


def audio_labels(f, t, lang_name=None):
    out = []
    for i, s in enumerate(_streams(f, "audio_streams")):
        label = _label(s, i + 1, t, lang_name)
        extra = " ".join(x for x in (_audio_format(s), _channels(s.get("channels"))) if x)
        out.append("%s (%s)" % (label, extra) if extra else label)
    return out


def _channels(ch):
    if not isinstance(ch, int) or ch <= 0:
        return ""
    return {1: "1.0", 2: "2.0", 6: "5.1", 8: "7.1"}.get(ch, "%dch" % ch)


class SubtitleChoice:
    """One row of the subtitle picker. kind: "off", "embedded" (index = the
    absolute stream index, image = a picture track), "external" (id)."""

    def __init__(
        self, kind, label, index=None, ext_id="", image=False, language="", forced=False, default=False, sdh=False
    ):
        self.kind = kind
        self.default = default
        self.sdh = sdh
        self.label = label
        self.index = index
        self.ext_id = ext_id
        self.image = image
        self.language = language
        self.forced = forced

    def __repr__(self):
        return "SubtitleChoice(%s %s %s)" % (
            self.kind,
            self.index if self.index is not None else self.ext_id,
            self.label,
        )


def subtitle_choices(f, t, session, lang_name=None):
    """Off, the file's own subtitle streams, then its external ones. In a
    session (session=True) a picture track is offered as a burn-in."""
    out = [SubtitleChoice("off", t("subtitles_off"))]
    for n, s in enumerate(_streams(f, "subtitle_streams"), 1):
        label = _label(s, n, t, lang_name)
        flags = []
        if s.get("forced"):
            flags.append(t("forced"))
        if s.get("sdh"):
            flags.append("SDH")
        image = bool(s.get("image_based"))
        if image and session:
            flags.append(t("burned_in"))
        if flags:
            label += " (%s)" % ", ".join(flags)
        out.append(
            SubtitleChoice(
                "embedded",
                label,
                index=s.get("index"),
                image=image,
                language=s.get("language") or "",
                forced=bool(s.get("forced")),
                default=bool(s.get("default")),
                sdh=bool(s.get("sdh")),
            )
        )
    for n, e in enumerate(_streams(f, "external_subtitles"), 1):
        if not e.get("id"):
            continue
        label = "%s (%s)" % (_label(e, n, t, lang_name), t("external"))
        out.append(
            SubtitleChoice(
                "external",
                label,
                ext_id=e["id"],
                language=e.get("language") or "",
                forced=bool(e.get("forced")),
                sdh=bool(e.get("sdh")),
            )
        )
    return out


def embedded_subtitle_position(f, stream_index):
    """Position of the subtitle stream with this absolute index among the
    file's subtitle streams: the number Kodi's subtitle list uses for a
    direct play (embedded tracks come first, in file order)."""
    for i, s in enumerate(_streams(f, "subtitle_streams")):
        if s.get("index") == stream_index:
            return i
    return None
