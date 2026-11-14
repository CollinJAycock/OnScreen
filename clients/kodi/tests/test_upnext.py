import base64
import json

from resources.lib.onscreen import upnext

S1 = [
    {"id": "e1", "type": "episode", "index": 1},
    {"id": "e2", "type": "episode", "index": 2, "index_end": 3},
    {"id": "e4", "type": "episode", "index": 4},
]


def test_next_in_season():
    assert upnext.next_in_season({"index": 1}, S1)["id"] == "e2"
    # A two-episode file (E02-E03) is followed by E04, not the E03 it covers.
    assert upnext.next_in_season({"index": 2, "index_end": 3}, S1)["id"] == "e4"
    assert upnext.next_in_season({"index": 4}, S1) is None
    assert upnext.next_in_season({}, S1) is None


def test_next_season_skips_specials():
    seasons = [
        {"id": "s0", "type": "season", "index": 0},
        {"id": "s3", "type": "season", "index": 3},
        {"id": "s2", "type": "season", "index": 2},
    ]
    assert upnext.next_season(1, seasons)["id"] == "s2"
    assert upnext.next_season(3, seasons) is None
    assert upnext.next_season(0, seasons)["id"] == "s2"  # after the specials: season 1+ only


class FakeClient:
    def __init__(self, items, children):
        self.items = items
        self.kids = children

    def item(self, item_id):
        return self.items[item_id]

    def children(self, item_id):
        return self.kids.get(item_id, [])


def test_find_next_crosses_into_the_next_season():
    c = FakeClient(
        {"s1": {"id": "s1", "index": 1, "parent_id": "show"}},
        {
            "s1": S1,
            "show": [{"id": "s1", "type": "season", "index": 1}, {"id": "s2", "type": "season", "index": 2}],
            "s2": [{"id": "x2", "type": "episode", "index": 2}, {"id": "x1", "type": "episode", "index": 1}],
        },
    )
    nxt, season = upnext.find_next(c, {"id": "e4", "index": 4, "parent_id": "s1", "grandparent_id": "show"})
    assert (nxt["id"], season) == ("x1", 2)
    nxt, season = upnext.find_next(c, {"id": "e1", "index": 1, "parent_id": "s1"})
    assert (nxt["id"], season) == ("e2", 1)
    assert upnext.find_next(c, {"id": "m", "index": 1}) == (None, None)


def test_episode_info_and_notification():
    def art(path, width):
        return "http://p/%s?w=%d" % (path, width)

    show = {"id": "show", "title": "Kodi Show", "poster_path": "TV/p.jpg", "fanart_path": "TV/f.jpg"}
    ep = {"id": "e2", "title": "Two", "index": 2, "duration_ms": 1500000, "thumb_path": "TV/e2.jpg", "watched": True}
    info = upnext.episode_info(ep, show, 1, art)
    assert info["episodeid"] == "e2" and info["tvshowid"] == "show" and info["showtitle"] == "Kodi Show"
    assert (info["season"], info["episode"], info["runtime"], info["playcount"]) == (1, 2, 1500, 1)
    assert info["art"] == {
        "thumb": "http://p/TV/e2.jpg?w=640",
        "tvshow.poster": "http://p/TV/p.jpg?w=500",
        "tvshow.fanart": "http://p/TV/f.jpg?w=1920",
    }
    req = upnext.notification("plugin.video.onscreen", {"a": 1}, info, "plugin://x/?action=play&id=e2")
    assert req["method"] == "JSONRPC.NotifyAll"
    assert req["params"]["sender"] == "plugin.video.onscreen.SIGNAL" and req["params"]["message"] == "upnext_data"
    data = json.loads(base64.b64decode(req["params"]["data"][0]))
    assert data["next_episode"]["episodeid"] == "e2" and data["play_url"].endswith("id=e2")


def test_unrated_episode_sends_no_rating():
    info = upnext.episode_info({"id": "e", "index": 1, "rating": 0}, {}, 1, lambda p, w: p)
    assert info["rating"] is None  # Up Next hides it; 0 would show "0.0"
    assert upnext.episode_info({"id": "e", "rating": 7.5}, {}, 1, lambda p, w: p)["rating"] == 7.5
