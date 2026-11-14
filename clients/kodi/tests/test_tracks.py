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


def test_preferred_subtitle_rules():
    subs = FILE["subtitle_streams"]
    assert tracks.preferred_subtitle(subs, "es", False)["index"] == 6  # forced wins in the language
    assert tracks.preferred_subtitle(subs, "en", False)["index"] == 4
    assert tracks.preferred_subtitle(subs, "en", True) is None  # forced only, none in English
    assert tracks.preferred_subtitle(subs, "", False)["index"] == 6  # the file's default
    assert tracks.preferred_subtitle([{"language": "en", "default": True}], "", True) is None


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
