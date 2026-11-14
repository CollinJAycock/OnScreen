"""The next episode, and the Up Next add-on's data for it.

Up Next (service.upnext, in Kodi's official repository) is how Kodi shows
"Next episode in 30 s" near the end of an episode. An add-on hands it the
current and next episode with a JSON-RPC notification ("upnext_data", the
JSON base64-encoded) once playback has started; Up Next plays play_url
when the time comes.

The next episode follows the TV apps' rule (Roku's Siblings.brs): the
episode numbered right after this one in its season (after the last one a
multi-episode file covers), else the first episode of the next season.
"""

import base64
import json


def _int(v):
    return v if isinstance(v, int) and not isinstance(v, bool) else None


def next_in_season(item, episodes):
    """The episode in episodes (its season's children) after item, or None."""
    idx = _int(item.get("index"))
    if idx is None:
        return None
    after = idx
    end = _int(item.get("index_end"))
    if end is not None and end > after:
        after = end
    for e in episodes or []:
        if isinstance(e, dict) and e.get("type") == "episode" and _int(e.get("index")) == after + 1:
            return e
    return None


def next_season(season_index, seasons):
    """The season after season_index among seasons (a show's children), or
    None. Specials (season 0) are never next."""
    later = [
        s
        for s in seasons or []
        if isinstance(s, dict)
        and s.get("type") == "season"
        and _int(s.get("index")) is not None
        and _int(s.get("index")) > max(season_index, 0)
    ]
    return min(later, key=lambda s: s["index"]) if later else None


def first_episode(episodes):
    eps = [e for e in episodes or [] if isinstance(e, dict) and e.get("type") == "episode" and _int(e.get("index"))]
    return min(eps, key=lambda e: e["index"]) if eps else None


def find_next(client, item):
    """(next episode, its season number) for an episode item (with its
    parent_id / grandparent_id), or (None, None). client is an api.Client."""
    season_id = item.get("parent_id")
    if not season_id:
        return None, None
    season = client.item(season_id)
    season_index = _int(season.get("index"))
    nxt = next_in_season(item, client.children(season_id))
    if nxt is not None:
        return nxt, season_index
    show_id = item.get("grandparent_id") or season.get("parent_id")
    if not show_id or season_index is None:
        return None, None
    s = next_season(season_index, client.children(show_id))
    if s is None:
        return None, None
    return first_episode(client.children(s["id"])), s["index"]


def episode_info(ep, show, season_number, art):
    """An episode as Up Next describes one. show: the show item (title,
    poster_path, fanart_path, logo_path); art(path, width) -> URL."""
    duration = _int(ep.get("duration_ms")) or 0
    images = {}
    thumb = ep.get("thumb_path") or ep.get("poster_path")
    if thumb:
        images["thumb"] = art(thumb, 640)
    for key, field, width in (
        ("tvshow.poster", "poster_path", 500),
        ("tvshow.fanart", "fanart_path", 1920),
        ("tvshow.clearlogo", "logo_path", 0),
    ):
        if show.get(field):
            images[key] = art(show[field], width)
    return {
        "episodeid": ep.get("id"),
        "tvshowid": show.get("id"),
        "title": ep.get("title") or "",
        "art": {k: v for k, v in images.items() if v},
        "season": season_number if season_number is not None else -1,
        "episode": _int(ep.get("index")) or -1,
        "showtitle": show.get("title") or "",
        "plot": ep.get("summary") or "",
        "playcount": 1 if (ep.get("watched") or ep.get("watch_state") == "watched") else 0,
        # Up Next hides the rating when it's None (0 would show "0.0").
        "rating": ep.get("rating") if isinstance(ep.get("rating"), (int, float)) and ep.get("rating") else None,
        "firstaired": (ep.get("originally_available_at") or "")[:10],
        "runtime": duration // 1000,
    }


def notification(sender, current, nxt, play_url):
    """The JSON-RPC request that hands Up Next its data."""
    data = {"current_episode": current, "next_episode": nxt, "play_url": play_url}
    encoded = base64.b64encode(json.dumps(data).encode("utf-8")).decode("ascii")
    return {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "JSONRPC.NotifyAll",
        "params": {"sender": sender + ".SIGNAL", "message": "upnext_data", "data": [encoded]},
    }
