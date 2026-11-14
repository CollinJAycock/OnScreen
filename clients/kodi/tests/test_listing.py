from resources.lib.onscreen import listing

T = {
    "season_n": "Season {n}",
    "specials": "Specials",
    "episode_n": "Episode {n}",
}


def t(key, **p):
    return T.get(key, key).format(**p)


def test_movie_entry():
    e = listing.entry(
        {
            "id": "m1",
            "type": "movie",
            "title": "Sintel",
            "year": 2010,
            "summary": "A girl and a dragon.",
            "genres": ["Animation"],
            "rating": 7.4,
            "duration_ms": 888000,
            "poster_path": "Movies/Sintel/poster.jpg",
            "watch_state": "in_progress",
            "view_offset_ms": 336000,
        },
        t,
    )
    assert e.playable and not e.folder and e.action == "play" and e.params == {"id": "m1"}
    assert e.mediatype == "movie" and not e.music
    assert e.info["title"] == "Sintel" and e.info["year"] == 2010 and e.info["duration"] == 888
    assert e.info["playcount"] == 0
    assert e.resume == (336.0, 888.0)
    assert e.art["poster"] == ("Movies/Sintel/poster.jpg", 500)
    keys = [c[0] for c in e.context]
    assert keys[:2] == ["play_version", "mark_watched"] and "add_watchlist" in keys and "add_favorite" in keys


def test_watched_item_has_no_resume_and_offers_unwatch():
    e = listing.entry(
        {"id": "m", "type": "movie", "title": "X", "watched": True, "view_offset_ms": 5000, "duration_ms": 9000}, t
    )
    assert e.info["playcount"] == 1 and e.resume is None
    assert ("mark_unwatched", "watched", {"id": "m", "on": "0"}) in e.context


def test_hub_episode_names_its_show():
    e = listing.entry(
        {
            "id": "e1",
            "type": "episode",
            "title": "The Dungeon",
            "show_title": "Delicious in Dungeon",
            "show_id": "s1",
            "season_number": 1,
            "episode_number": 4,
            "thumb_path": "TV/D/S01E04.jpg",
            "poster_path": "TV/D/poster.jpg",
        },
        t,
        "continue",
    )
    assert e.label == "Delicious in Dungeon · S01E04 · The Dungeon"
    assert e.info["season"] == 1 and e.info["episode"] == 4 and e.info["tvshowtitle"] == "Delicious in Dungeon"
    assert e.art["thumb"] == ("TV/D/S01E04.jpg", 640)
    keys = [c[0] for c in e.context]
    assert "go_to_show" in keys and "remove_continue" in keys


def test_season_listing_episode_is_number_and_title():
    e = listing.entry({"id": "e", "type": "episode", "title": "Pilot", "index": 1}, t)
    assert e.label == "1. Pilot" and e.info["episode"] == 1


def test_seasons_and_show_counts():
    assert listing.entry({"id": "s0", "type": "season", "index": 0}, t).label == "Specials"
    s = listing.entry({"id": "s2", "type": "season", "index": 2}, t)
    assert s.folder and s.label == "Season 2" and s.action == "children"
    show = listing.entry({"id": "sh", "type": "show", "title": "S", "leaf_count": 10, "unwatched_count": 0}, t)
    assert show.properties == {"TotalEpisodes": "10", "UnWatchedEpisodes": "0", "WatchedEpisodes": "10"}
    assert show.info["playcount"] == 1


def test_music_entries():
    track = listing.entry(
        {"id": "t", "type": "track", "title": "Song", "index": 3, "disc_number": 2, "duration_ms": 200000}, t
    )
    assert track.music and track.mediatype == "song" and track.label == "3. Song"
    assert track.info["tracknumber"] == 3 and track.info["discnumber"] == 2
    album = listing.entry({"id": "a", "type": "album", "title": "LP"}, t)
    assert album.folder and album.music


def test_unknown_types_are_skipped():
    assert listing.entry({"id": "p", "type": "photo"}, t) is None
    assert listing.entry({"type": "movie"}, t) is None
    assert listing.entries([{"id": "p", "type": "book"}, {"id": "m", "type": "movie", "title": "M"}], t)[0].id == "m"


def test_content_for():
    movies = listing.entries([{"id": "1", "type": "movie"}, {"id": "2", "type": "movie"}], t)
    assert listing.content_for(movies) == "movies"
    mixed = listing.entries([{"id": "1", "type": "movie"}, {"id": "2", "type": "episode"}], t)
    assert listing.content_for(mixed) == "videos"
    songs = listing.entries([{"id": "1", "type": "track"}, {"id": "2", "type": "audiobook_chapter"}], t)
    assert listing.content_for(songs) == "songs"
    assert listing.content_for([]) == ""


def test_libraries_for_sections():
    libs = [
        {"id": "1", "type": "movie"},
        {"id": "2", "type": "music"},
        {"id": "3", "type": "photo"},
        {"id": "4", "type": "anime"},
        {"id": "5", "type": "audiobook"},
    ]
    assert [x["id"] for x in listing.libraries_for(libs, "video")] == ["1", "4"]
    assert [x["id"] for x in listing.libraries_for(libs, "audio")] == ["2", "5"]
    assert [x["id"] for x in listing.libraries_for(libs, "")] == ["1", "2", "4", "5"]


def test_watchable_rows_filter_by_section():
    hub = {
        "continue_watching": [{"id": "e", "type": "episode"}, {"id": "t", "type": "track"}],
        "next_up": [],
        "recently_added": [{"id": "a", "type": "album"}],
    }
    assert listing.watchable_rows(hub, "video") == [("continue_watching", [{"id": "e", "type": "episode"}])]
    assert [k for k, _ in listing.watchable_rows(hub, "audio")] == ["continue_watching", "recently_added"]


def test_resume_point_needs_a_duration():
    """Kodi only treats a resume point with a total as resumable (checked on
    Kodi 21): without duration_ms the play action asks instead."""
    assert listing.entry({"id": "m", "type": "movie", "title": "X", "view_offset_ms": 41875}, t).resume is None
    e = listing.entry({"id": "m", "type": "movie", "title": "X", "view_offset_ms": 15000, "duration_ms": 60000}, t)
    assert e.resume == (15.0, 60.0)
    assert (
        listing.entry(
            {"id": "m", "type": "movie", "title": "X", "view_offset_ms": 4000, "duration_ms": 60000}, t
        ).resume
        is None
    )
    near_end = {"id": "m", "type": "movie", "title": "X", "view_offset_ms": 590000, "duration_ms": 600000}
    assert listing.entry(near_end, t).resume is None


def test_show_and_season_pass_their_context_down():
    show = listing.entry({"id": "sh", "type": "show", "title": "Kodi Show"}, t)
    assert show.params == {"id": "sh", "show": "Kodi Show"}
    season = listing.entry({"id": "s1", "type": "season", "index": 1}, t, parent={"show": "Kodi Show"})
    assert season.params == {"id": "s1", "season": "1", "show": "Kodi Show"}
    assert season.info["tvshowtitle"] == "Kodi Show"
    ep = listing.entry(
        {"id": "e", "type": "episode", "title": "Pilot", "index": 1}, t, parent={"show": "Kodi Show", "season": 1}
    )
    assert ep.info["season"] == 1 and ep.info["episode"] == 1 and ep.info["tvshowtitle"] == "Kodi Show"
    assert ep.label == "1. Pilot"  # the season listing's own label, no show name
