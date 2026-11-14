"""Entry (resources/lib/onscreen/listing.py) -> xbmcgui.ListItem."""

import urllib.parse

import xbmcgui

from resources.lib.kodi import common
from resources.lib.onscreen import artproxy

BASE = "plugin://%s/" % common.ADDON_ID


def plugin_url(action, **params):
    q = {"action": action}
    q.update({k: v for k, v in params.items() if v not in (None, "")})
    return BASE + "?" + urllib.parse.urlencode(q)


class ArtResolver:
    """Server artwork paths as image URLs: through the service's artwork
    proxy when it runs (stable URLs, see onscreen/artproxy.py), else straight
    from the server with the 24 h asset token."""

    def __init__(self, server_url, asset_token):
        self.server_url = server_url
        self.asset_token = asset_token
        self.port = 0
        if common.setting_bool("art_proxy"):
            try:
                self.port = int(xbmcgui.Window(10000).getProperty(common.PROP_ART_PORT) or 0)
            except ValueError:
                self.port = 0

    def url(self, path, width=0):
        if not path:
            return ""
        if path.startswith("http://") or path.startswith("https://"):
            return path
        if self.port:
            return artproxy.proxy_url(self.port, path, width)
        url = self.server_url + "/artwork/" + urllib.parse.quote(path.lstrip("/"), safe="/")
        q = []
        if width:
            q.append("w=%d" % width)
        if self.asset_token:
            q.append("token=" + urllib.parse.quote(self.asset_token, safe=""))
        return url + ("?" + "&".join(q) if q else "")


def _call(obj, name, *args):
    """obj.name(*args) when this Kodi has the setter (they arrived over
    several versions)."""
    fn = getattr(obj, name, None)
    if fn is None:
        return
    try:
        fn(*args)
    except (TypeError, ValueError, RuntimeError):
        pass


def video_tag(li, info, mediatype, resume=None):
    tag = li.getVideoInfoTag()
    _call(tag, "setMediaType", mediatype)
    if "title" in info:
        _call(tag, "setTitle", info["title"])
    if "originaltitle" in info:
        _call(tag, "setOriginalTitle", info["originaltitle"])
    if "year" in info:
        _call(tag, "setYear", info["year"])
    if "plot" in info:
        _call(tag, "setPlot", info["plot"])
    if "genres" in info:
        _call(tag, "setGenres", info["genres"])
    if "rating" in info:
        _call(tag, "setRating", info["rating"])
    if "premiered" in info:
        _call(tag, "setPremiered", info["premiered"])
    if "duration" in info:
        _call(tag, "setDuration", info["duration"])
    if "season" in info:
        _call(tag, "setSeason", info["season"])
    if "episode" in info:
        _call(tag, "setEpisode", info["episode"])
    if "tvshowtitle" in info:
        _call(tag, "setTvShowTitle", info["tvshowtitle"])
    if "playcount" in info:
        _call(tag, "setPlaycount", info["playcount"])
    if resume:
        _call(tag, "setResumePoint", resume[0], resume[1])
    return tag


def music_tag(li, info, mediatype):
    tag = li.getMusicInfoTag()
    _call(tag, "setMediaType", mediatype)
    if "title" in info:
        _call(tag, "setTitle", info["title"])
    if "year" in info:
        _call(tag, "setYear", info["year"])
    if "genres" in info:
        _call(tag, "setGenres", info["genres"])
    if "duration" in info:
        _call(tag, "setDuration", info["duration"])
    if "tracknumber" in info:
        _call(tag, "setTrack", info["tracknumber"])
    if "discnumber" in info:
        _call(tag, "setDisc", info["discnumber"])
    if "plot" in info:
        _call(tag, "setComment", info["plot"])
    if "playcount" in info:
        _call(tag, "setPlayCount", info["playcount"])
    return tag


def from_entry(e, art):
    """(url, ListItem, is_folder) for an Entry. A resumable entry's URL says
    so (r=1): the play action then knows Kodi showed its own resume
    dialog."""
    params = dict(e.params)
    if e.resume:
        params["r"] = "1"
    url = plugin_url(e.action, **params)
    li = xbmcgui.ListItem(label=e.label, label2=e.label2, offscreen=True)
    if e.music:
        music_tag(li, e.info, e.mediatype)
    else:
        video_tag(li, e.info, e.mediatype, e.resume)
    images = {}
    for key, (path, width) in e.art.items():
        u = art.url(path, width)
        if u:
            images[key] = u
    if images:
        if "icon" not in images and "thumb" in images:
            images["icon"] = images["thumb"]
        li.setArt(images)
    for k, v in e.properties.items():
        li.setProperty(k, v)
    if e.playable:
        li.setProperty("IsPlayable", "true")
    menu = []
    for key, action, params in e.context:
        target = plugin_url(action, **params)
        if action == "children":
            menu.append((common.t(key), "Container.Update(%s)" % target))
        elif action == "play":
            menu.append((common.t(key), "PlayMedia(%s)" % target))
        else:
            menu.append((common.t(key), "RunPlugin(%s)" % target))
    if menu:
        li.addContextMenuItems(menu)
    return url, li, e.folder


def folder(label, action, art=None, plot="", **params):
    """A plain menu folder (root menu, library menu)."""
    li = xbmcgui.ListItem(label=label, offscreen=True)
    if plot:
        _call(li.getVideoInfoTag(), "setPlot", plot)
    li.setArt(art or {"icon": "DefaultFolder.png", "thumb": "DefaultFolder.png"})
    return plugin_url(action, **params), li, True
