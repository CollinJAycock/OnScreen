"""OnScreen items as Kodi list entries, without Kodi.

entry() takes any of the item shapes the API returns (library page, item
children, hub row, search result, watchlist row) and describes the list item:
its label, whether it opens a folder or plays, the info tag fields, the
artwork paths and the context-menu actions. resources/lib/kodi/listitems.py
turns an Entry into an xbmcgui.ListItem.
"""

from resources.lib.onscreen import playback

PLAYABLE = frozenset(
    (
        "movie",
        "episode",
        "music_video",
        "home_video",
        "video",
        "track",
        "audiobook_chapter",
        "podcast_episode",
    )
)

FOLDERS = frozenset(("show", "season", "artist", "album", "book_author", "book_series", "audiobook", "podcast"))

# Item types that carry their own watch state on the server (POST /watched
# takes these, and seasons / shows mark every episode).
WATCHABLE = frozenset(("movie", "episode", "music_video", "home_video", "season", "show"))

MUSIC_TYPES = frozenset(
    (
        "artist",
        "album",
        "track",
        "book_author",
        "book_series",
        "audiobook",
        "audiobook_chapter",
        "podcast",
        "podcast_episode",
    )
)

# Kodi mediatype per item type.
MEDIATYPE = {
    "movie": "movie",
    "show": "tvshow",
    "season": "season",
    "episode": "episode",
    "music_video": "musicvideo",
    "home_video": "video",
    "video": "video",
    "artist": "artist",
    "album": "album",
    "track": "song",
    "book_author": "artist",
    "book_series": "album",
    "audiobook": "album",
    "audiobook_chapter": "song",
    "podcast": "album",
    "podcast_episode": "song",
}

# setContent value for a listing whose items are of one type.
CONTENT = {
    "movie": "movies",
    "show": "tvshows",
    "season": "seasons",
    "episode": "episodes",
    "music_video": "musicvideos",
    "artist": "artists",
    "album": "albums",
    "track": "songs",
    "book_author": "artists",
    "book_series": "albums",
    "audiobook": "albums",
    "audiobook_chapter": "songs",
    "podcast": "albums",
    "podcast_episode": "songs",
}

# Library types the add-on lists, by the Kodi section it was opened from
# (the add-on provides video and audio). Photo, book and manga libraries
# have no Kodi player here.
VIDEO_LIBRARIES = frozenset(("movie", "show", "anime", "cartoons", "home_video"))
AUDIO_LIBRARIES = frozenset(("music", "audiobook", "podcast"))


class Entry:
    def __init__(self, item_id, item_type, label):
        self.id = item_id
        self.type = item_type
        self.label = label
        self.label2 = ""
        self.folder = False
        self.playable = False
        self.action = ""
        self.params = {}
        self.mediatype = MEDIATYPE.get(item_type, "video")
        self.music = item_type in MUSIC_TYPES
        self.info = {}
        self.resume = None  # (position_sec, total_sec)
        self.art = {}  # Kodi art key -> (server path, width or 0 for the original)
        self.properties = {}
        self.context = []  # (string key, action, params)

    def __repr__(self):
        return "Entry(%s %s %r)" % (self.type, self.id, self.label)


def _int(v):
    return v if isinstance(v, int) and not isinstance(v, bool) else None


def _num(v):
    return v if isinstance(v, (int, float)) and not isinstance(v, bool) else None


def _str(v):
    return v if isinstance(v, str) else ""


def episode_code(season, episode, episode_end=None):
    """ "S01E02", "S01E02-E03"; "" without both numbers."""
    if season is None or episode is None:
        return ""
    code = "S%02dE%02d" % (season, episode)
    if episode_end is not None and episode_end > episode:
        code += "-E%02d" % episode_end
    return code


def label_for(item, t):
    """The list label. An episode in a mixed row (a hub row, search) names
    its show; one in a season listing is just its number and title."""
    typ = _str(item.get("type"))
    title = _str(item.get("title"))
    if typ == "episode":
        season = _int(item.get("season_number"))
        number = _int(item.get("episode_number"))
        if number is None:
            number = _int(item.get("index"))
        code = episode_code(season, number, _int(item.get("episode_number_end")) or _int(item.get("index_end")))
        show = _str(item.get("show_title")) or _str(item.get("grandparent_title"))
        if show and code:
            return "%s · %s · %s" % (show, code, title) if title else "%s · %s" % (show, code)
        if show:
            return "%s · %s" % (show, title)
        if number is not None and season is None:
            return "%d. %s" % (number, title) if title else t("episode_n", n=number)
        return title
    if typ == "season":
        idx = _int(item.get("index"))
        if title:
            return title
        if idx == 0:
            return t("specials")
        if idx is not None:
            return t("season_n", n=idx)
    if typ in ("track", "audiobook_chapter"):
        idx = _int(item.get("index"))
        if idx is not None and title:
            return "%d. %s" % (idx, title)
    return title


def entry(item, t, context_row="", parent=None):
    """An Entry for item, or None for a type the add-on doesn't show.
    context_row names the row it's listed in ("continue" adds "Remove from
    Continue Watching"). parent is what the folder knows about its
    children that they don't say themselves: {"show": title, "season": n}
    (a season's episodes carry neither)."""
    parent = parent or {}
    if not isinstance(item, dict) or not _str(item.get("id")):
        return None
    typ = _str(item.get("type"))
    if typ not in PLAYABLE and typ not in FOLDERS:
        return None
    e = Entry(item["id"], typ, label_for(item, t))
    if typ in PLAYABLE:
        e.playable = True
        e.action = "play"
    else:
        e.folder = True
        e.action = "children"
    e.params = {"id": item["id"]}

    info = e.info
    info["title"] = _str(item.get("title"))
    year = _int(item.get("year"))
    if year:
        info["year"] = year
    plot = _str(item.get("summary"))
    if plot:
        info["plot"] = plot
    if _str(item.get("original_title")):
        info["originaltitle"] = item["original_title"]
    genres = item.get("genres")
    if isinstance(genres, list):
        info["genres"] = [g for g in genres if isinstance(g, str)]
    rating = _num(item.get("rating"))
    if rating is not None:
        info["rating"] = float(rating)
    aired = _str(item.get("originally_available_at"))
    if aired:
        info["premiered"] = aired[:10]
    duration = _int(item.get("duration_ms"))
    if duration:
        info["duration"] = duration // 1000

    idx = _int(item.get("index"))
    if typ == "episode":
        season = _int(item.get("season_number"))
        number = _int(item.get("episode_number"))
        if number is None:
            number = idx
        if season is None:
            season = _int(parent.get("season"))
        if season is not None:
            info["season"] = season
        if number is not None:
            info["episode"] = number
        show = _str(item.get("show_title")) or _str(item.get("grandparent_title")) or _str(parent.get("show"))
        if show:
            info["tvshowtitle"] = show
    elif typ == "season":
        if idx is not None:
            info["season"] = idx
            e.params["season"] = str(idx)
        if _str(parent.get("show")):
            info["tvshowtitle"] = parent["show"]
            e.params["show"] = parent["show"]
    elif typ == "show" and info["title"]:
        e.params["show"] = info["title"]
    elif typ in ("track", "audiobook_chapter", "podcast_episode") and idx is not None:
        info["tracknumber"] = idx
        disc = _int(item.get("disc_number"))
        if disc:
            info["discnumber"] = disc

    # Watch state: library pages say watch_state, children say watched.
    watched = item.get("watched") is True or _str(item.get("watch_state")) == "watched"
    if typ in WATCHABLE or typ in PLAYABLE:
        info["playcount"] = 1 if watched else 0
    # A resume point makes Kodi ask "Resume from ..." itself (and draw the
    # progress bar). Same rule as the play action (playback.resume_offset_ms),
    # so Kodi never offers a resume the add-on would then ignore. Kodi only
    # treats a point with a total as resumable, so an item without
    # duration_ms (one the server hasn't matched; only its file has a
    # length) gets none, and the play action asks instead.
    if typ in PLAYABLE and not watched and duration and playback.resume_offset_ms(item, duration):
        e.resume = (item["view_offset_ms"] / 1000.0, duration / 1000.0)
    leaves = _int(item.get("leaf_count"))
    unwatched = _int(item.get("unwatched_count"))
    if typ in ("show", "season") and leaves is not None:
        e.properties["TotalEpisodes"] = str(leaves)
        if unwatched is not None:
            e.properties["UnWatchedEpisodes"] = str(unwatched)
            e.properties["WatchedEpisodes"] = str(max(0, leaves - unwatched))
        if unwatched == 0 and leaves > 0:
            info["playcount"] = 1

    poster = _str(item.get("poster_path"))
    thumb = _str(item.get("thumb_path"))
    fanart = _str(item.get("fanart_path"))
    if typ == "episode" and thumb:
        e.art["thumb"] = (thumb, 640)
        if poster:
            e.art["poster"] = (poster, 500)
    else:
        if poster:
            e.art["poster"] = (poster, 500)
            e.art["thumb"] = (poster, 500)
        elif thumb:
            e.art["thumb"] = (thumb, 640)
    if fanart:
        e.art["fanart"] = (fanart, 1920)
    logo = _str(item.get("logo_path"))
    if logo:
        e.art["clearlogo"] = (logo, 0)

    _add_context(e, item, watched, context_row)
    return e


def _add_context(e, item, watched, context_row):
    typ = e.type
    if typ in ("movie", "episode", "music_video", "home_video", "video"):
        e.context.append(("play_version", "play", {"id": e.id, "choose": "1"}))
    if typ in WATCHABLE:
        if watched:
            e.context.append(("mark_unwatched", "watched", {"id": e.id, "on": "0"}))
        else:
            e.context.append(("mark_watched", "watched", {"id": e.id, "on": "1"}))
    if typ == "episode":
        show_id = _str(item.get("show_id")) or _str(item.get("grandparent_id"))
        if show_id:
            e.context.append(("go_to_show", "children", {"id": show_id}))
    if context_row != "watchlist" and typ in ("movie", "show", "episode", "season", "music_video", "home_video"):
        e.context.append(("add_watchlist", "watchlist_set", {"id": e.id, "status": "plan_to_watch"}))
    if context_row == "watchlist":
        e.context.append(("remove_watchlist", "watchlist_set", {"id": e.id, "status": ""}))
    if context_row == "continue":
        e.context.append(("remove_continue", "dismiss", {"id": e.id}))
    if typ in PLAYABLE or typ in FOLDERS:
        if context_row == "favorites":
            e.context.append(("remove_favorite", "favorite", {"id": e.id, "on": "0"}))
        else:
            e.context.append(("add_favorite", "favorite", {"id": e.id, "on": "1"}))


def entries(items, t, context_row="", parent=None):
    out = []
    for it in items or []:
        e = entry(it, t, context_row, parent)
        if e is not None:
            out.append(e)
    return out


def content_for(entries_):
    """setContent value for a listing: the one type's content, "videos" or
    "songs" for a mix, "" (files) when empty."""
    types = {e.type for e in entries_}
    if not types:
        return ""
    if len(types) == 1:
        return CONTENT.get(types.pop(), "videos")
    if all(e.music for e in entries_):
        return "songs"
    if types <= {"movie", "episode", "music_video", "home_video", "video", "show", "season"}:
        return "videos"
    return "videos"


def libraries_for(libraries, section):
    """The libraries to list in a Kodi section ("video", "audio", or ""
    for both), in the server's order."""
    if section == "audio":
        allowed = AUDIO_LIBRARIES
    elif section == "video":
        allowed = VIDEO_LIBRARIES
    else:
        allowed = VIDEO_LIBRARIES | AUDIO_LIBRARIES
    return [
        lib
        for lib in libraries or []
        if isinstance(lib, dict) and _str(lib.get("type")) in allowed and _str(lib.get("id"))
    ]


def watchable_rows(hub, section):
    """The home rows to list, as (row key, items). Audio sections get no
    video rows. Rows the server sent empty are left out."""
    if not isinstance(hub, dict):
        return []
    keys = ("continue_watching", "next_up", "recently_added", "plan_to_watch", "trending")
    rows = []
    for key in keys:
        items = hub.get(key)
        if not isinstance(items, list) or not items:
            continue
        if section == "audio":
            items = [i for i in items if isinstance(i, dict) and _str(i.get("type")) in MUSIC_TYPES]
        elif section == "video":
            items = [i for i in items if isinstance(i, dict) and _str(i.get("type")) not in MUSIC_TYPES]
        if items:
            rows.append((key, items))
    return rows
