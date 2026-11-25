from resources.lib.onscreen import tracks


def t(key, **p):
    return {
        "subtitles_off": "Off",
        "forced": "forced",
        "burned_in": "burned in",
        "external": "external",
        "track_n": "Track {n}",
    }[key].format(**p)


FILE = {
    "id": "f",
    "audio_streams": [
        {"index": 1, "codec": "truehd", "profile": "Dolby TrueHD + Dolby Atmos", "channels": 8, "language": "eng"},
        {"index": 2, "codec": "ac3", "channels": 6, "language": "spa", "title": "Castellano"},
        {"index": 3, "codec": "aac", "channels": 2, "language": ""},
    ],
    "subtitle_streams": [
        {"index": 4, "codec": "subrip", "language": "eng"},
        {"index": 5, "codec": "hdmv_pgs_subtitle", "language": "spa", "image_based": True},
        {"index": 6, "codec": "subrip", "language": "spa", "forced": True, "default": True},
    ],
    "external_subtitles": [{"id": "x1", "language": "fr", "forced": False}],
}


def test_normalize_and_match():
    assert tracks.normalize_lang("ENG") == "en"
    assert tracks.normalize_lang("pt_BR") == "pt"
    assert tracks.normalize_lang("xyz") == "xyz"
    assert tracks.lang_matches("ger", "de-AT")
    assert not tracks.lang_matches("", "")
    assert not tracks.lang_matches("eng", "spa")


def test_preferred_audio():
    assert tracks.preferred_audio(FILE, "es") == 1
    assert tracks.preferred_audio(FILE, "en") == 0
    assert tracks.preferred_audio(FILE, "ja") is None
    assert tracks.preferred_audio(FILE, None) is None


def _idx(subs, lang, mode, audio=None):
    s = tracks.preferred_subtitle(subs, lang, mode, audio)
    return s["index"] if s else None


def test_preferred_subtitle_rules():
    subs = FILE["subtitle_streams"]
    assert _idx(subs, "es", "always") == 5  # the full track before the forced one
    assert _idx(subs, "es", "forced") == 6
    assert _idx(subs, "en", "always") == 4
    assert _idx(subs, "en", "forced") is None  # forced only, none in English
    assert _idx(subs, "", "always") == 6  # no preference: the file's default
    assert _idx(subs, "", "foreign") == 6  # ... a forced one under the forced modes
    assert _idx([{"index": 1, "language": "en", "default": True}], "", "forced") is None
    assert _idx(subs, "off", "always") is None  # the "Off" preference matches nothing


# docs/foreign-audio-subtitles.md section 2, row by row.
BOTH = [{"index": 2, "language": "eng"}, {"index": 3, "language": "eng", "forced": True}]
FULL = [{"index": 2, "language": "eng"}]
SPANISH = [{"index": 2, "language": "spa"}]


def test_foreign_mode_table():
    assert _idx(BOTH, "en", "foreign", "eng") == 3
    assert _idx(FULL, "en", "foreign", "eng") is None
    assert _idx(BOTH, "en", "foreign", "jpn") == 2
    assert _idx(FULL, "en", "foreign", "jpn") == 2
    assert _idx(SPANISH, "en", "foreign", "jpn") is None
    assert _idx(FULL, "en", "foreign", "und") is None  # untagged audio isn't foreign
    assert _idx(FULL, "en", "foreign", "") is None
    assert _idx(FULL, "en", "foreign", None) is None
    assert _idx(BOTH, "en-US", "foreign", "fre") == 2


def test_sdh_and_always():
    sdh = [{"index": 2, "language": "eng", "sdh": True}, {"index": 3, "language": "eng"}]
    assert _idx(sdh, "en", "always") == 3  # plain before SDH
    assert _idx(sdh[:1], "en", "foreign", "jpn") == 2  # SDH when it's the only full one
    assert _idx([{"index": 3, "language": "eng", "forced": True}], "en", "always") == 3


def test_subtitle_mode_of():
    assert tracks.subtitle_mode_of({"subtitle_mode": "foreign", "forced_subtitles_only": True}) == "foreign"
    assert tracks.subtitle_mode_of({"subtitle_mode": "always"}) == "always"
    assert tracks.subtitle_mode_of({"forced_subtitles_only": True}) == "forced"  # a server before 00066
    assert tracks.subtitle_mode_of({"forced_subtitles_only": False}) == "always"
    assert tracks.subtitle_mode_of({"subtitle_mode": "sometimes"}) == "always"
    assert tracks.subtitle_mode_of(None) == "always"


def test_foreign_audio_and_playing_language():
    assert tracks.is_foreign_audio("jpn", "en")
    assert not tracks.is_foreign_audio("eng", "en")
    assert not tracks.is_foreign_audio("mul", "en")
    dual = {"audio_streams": [{"language": "jpn"}, {"language": "eng", "default": True}]}
    assert tracks.default_audio(dual) == 1
    assert tracks.default_audio({"audio_streams": [{"language": "jpn"}]}) == 0
    assert tracks.playing_audio_language(dual, None, session=False) == "eng"  # Kodi plays the default
    assert tracks.playing_audio_language(dual, None, session=True) == "jpn"  # the server maps 0:a:0
    assert tracks.playing_audio_language(dual, 0, session=False) == "jpn"
    assert tracks.playing_audio_language({}, None, session=False) == ""


def test_subtitle_choices_and_positions():
    direct = tracks.subtitle_choices(FILE, t, session=False)
    assert [c.label for c in direct] == ["Off", "eng", "spa", "spa (forced)", "fr (external)"]
    session = tracks.subtitle_choices(FILE, t, session=True)
    assert session[2].label == "spa (burned in)" and session[2].image and session[2].index == 5
    assert session[4].kind == "external" and session[4].ext_id == "x1"
    assert tracks.embedded_subtitle_position(FILE, 6) == 2
    assert tracks.embedded_subtitle_position(FILE, 99) is None


def test_audio_labels():
    assert tracks.audio_labels(FILE, t) == [
        "eng (Dolby TrueHD + Dolby Atmos 7.1)",
        "spa · Castellano (AC3 5.1)",
        "Track 3 (AAC 2.0)",
    ]


def test_short_profiles_give_way_to_the_codec():
    f = {"audio_streams": [{"codec": "aac", "profile": "LC", "channels": 1, "language": "eng"}]}
    assert tracks.audio_labels(f, t) == ["eng (AAC 1.0)"]


def test_language_names_and_redundant_titles():
    def names(code):
        return {"eng": "English", "spa": "Spanish"}.get(code, "")

    assert tracks.audio_labels(FILE, t, names)[:2] == [
        "English (Dolby TrueHD + Dolby Atmos 7.1)",
        "Spanish · Castellano (AC3 5.1)",
    ]
    f = {"audio_streams": [{"codec": "aac", "language": "eng", "title": "English", "channels": 2}]}
    assert tracks.audio_labels(f, t, names) == ["English (AAC 2.0)"]  # the title only repeated it
    assert [c.label for c in tracks.subtitle_choices(FILE, t, True, names)][1:3] == ["English", "Spanish (burned in)"]
