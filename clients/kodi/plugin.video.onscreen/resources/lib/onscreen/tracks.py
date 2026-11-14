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


def preferred_subtitle(subs, preferred_lang, forced_only):
    """The subtitle to turn on, or None for off: a forced track in the
    preferred language first, else the first in it (only forced ones under
    forced_only); with no preference, the track the file marks default
    (under forced_only only when it's forced). Mirrors pickPreferredSubtitle."""
    subs = [s for s in subs if isinstance(s, dict)]
    if not preferred_lang:
        default = next((s for s in subs if s.get("default")), None)
        if default is None or (forced_only and not default.get("forced")):
            return None
        return default
    in_lang = [s for s in subs if lang_matches(s.get("language"), preferred_lang)]
    forced = next((s for s in in_lang if s.get("forced")), None)
    if forced_only:
        return forced
    return forced or (in_lang[0] if in_lang else None)


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

    def __init__(self, kind, label, index=None, ext_id="", image=False, language="", forced=False, default=False):
        self.kind = kind
        self.default = default
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
            )
        )
    for n, e in enumerate(_streams(f, "external_subtitles"), 1):
        if not e.get("id"):
            continue
        label = "%s (%s)" % (_label(e, n, t, lang_name), t("external"))
        out.append(
            SubtitleChoice(
                "external", label, ext_id=e["id"], language=e.get("language") or "", forced=bool(e.get("forced"))
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
