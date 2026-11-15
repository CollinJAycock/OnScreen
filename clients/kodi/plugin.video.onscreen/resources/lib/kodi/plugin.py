"""The plugin's router: plugin://plugin.video.onscreen/?action=...

Kodi starts a fresh interpreter for every call, with sys.argv = [base URL,
handle, "?query", "resume:true|false"]. Folder actions list a directory;
play resolves a stream; the rest (sign in, mark watched...) run from the
context menu or settings with RunPlugin and end by refreshing the listing.
"""

import sys
import urllib.parse

import xbmc
import xbmcgui
import xbmcplugin

from resources.lib.kodi import auth, common, listitems, player
from resources.lib.onscreen import api, listing, playback

SORTS = {
    "movies": (
        xbmcplugin.SORT_METHOD_UNSORTED,
        xbmcplugin.SORT_METHOD_LABEL_IGNORE_THE,
        xbmcplugin.SORT_METHOD_VIDEO_YEAR,
        xbmcplugin.SORT_METHOD_VIDEO_RATING,
        xbmcplugin.SORT_METHOD_DURATION,
    ),
    "tvshows": (
        xbmcplugin.SORT_METHOD_UNSORTED,
        xbmcplugin.SORT_METHOD_LABEL_IGNORE_THE,
        xbmcplugin.SORT_METHOD_VIDEO_YEAR,
    ),
    "episodes": (xbmcplugin.SORT_METHOD_UNSORTED, xbmcplugin.SORT_METHOD_EPISODE, xbmcplugin.SORT_METHOD_LABEL),
    "songs": (xbmcplugin.SORT_METHOD_UNSORTED, xbmcplugin.SORT_METHOD_TRACKNUM, xbmcplugin.SORT_METHOD_LABEL),
    "albums": (xbmcplugin.SORT_METHOD_UNSORTED, xbmcplugin.SORT_METHOD_LABEL, xbmcplugin.SORT_METHOD_VIDEO_YEAR),
    "artists": (xbmcplugin.SORT_METHOD_UNSORTED, xbmcplugin.SORT_METHOD_LABEL),
}

ROW_LABELS = {
    "continue_watching": "continue_watching",
    "next_up": "next_up",
    "recently_added": "recently_added",
    "trending": "trending",
}


class Router:
    def __init__(self, argv):
        self.base = argv[0]
        self.handle = int(argv[1]) if len(argv) > 1 and argv[1].lstrip("-").isdigit() else -1
        query = argv[2][1:] if len(argv) > 2 and argv[2].startswith("?") else (argv[2] if len(argv) > 2 else "")
        self.params = dict(urllib.parse.parse_qsl(query))
        self.resume = argv[3] if len(argv) > 3 else ""
        # Kodi adds content_type when the add-on is opened from a section
        # (it provides video, audio and pictures); deeper URLs carry it on
        # as "s".
        self.section = self.params.get("s") or {"video": "video", "audio": "audio", "image": "image"}.get(
            self.params.get("content_type", ""), ""
        )
        self._client = None

    @property
    def client(self):
        if self._client is None:
            self._client = common.client()
        return self._client

    def run(self):
        action = self.params.get("action", "")
        fn = getattr(self, "do_" + action, None) if action else self.root
        if fn is None:
            common.log("unknown action %r" % action, xbmc.LOGWARNING)
            return self.end(False)
        try:
            fn()
        except api.ApiError as e:
            common.log("%s: %s %s" % (action or "root", e.status, e), xbmc.LOGWARNING)
            if e.signed_out:
                common.notify(common.t("session_expired"), icon=xbmcgui.NOTIFICATION_WARNING)
            elif e.code == api.CLEARTEXT_REMOTE:
                host = api.url_host(common.server_url())
                common.notify(common.t("server_cleartext", host=host), icon=xbmcgui.NOTIFICATION_ERROR, ms=10000)
            else:
                common.notify(e.message or common.t("error"), icon=xbmcgui.NOTIFICATION_ERROR)
            if action != "play" and self.handle >= 0:
                self.end(False)
            elif action == "play":
                xbmcplugin.setResolvedUrl(self.handle, False, xbmcgui.ListItem())

    # ── helpers ──

    def url(self, action, **params):
        if self.section:
            params.setdefault("s", self.section)
        return listitems.plugin_url(action, **params)

    def add(self, items):
        """items: [(url, ListItem, is_folder)]"""
        if items:
            xbmcplugin.addDirectoryItems(self.handle, items, len(items))

    def end(self, ok=True, content="", cache=True):
        if content:
            xbmcplugin.setContent(self.handle, content)
            for method in SORTS.get(content, (xbmcplugin.SORT_METHOD_UNSORTED,)):
                xbmcplugin.addSortMethod(self.handle, method)
        xbmcplugin.endOfDirectory(self.handle, succeeded=ok, cacheToDisc=cache)

    def art(self):
        return listitems.ArtResolver(self.client.server_url, self.client.store.load().get("asset_token", ""))

    def list_entries(self, items, row="", extra=None, parent=None):
        es = listing.entries(items, common.t, row, parent)
        art = self.art()
        out = [listitems.from_entry(e, art) for e in es]
        for i, (url, li, folder) in enumerate(out):
            if self.section:
                out[i] = (url + "&s=" + self.section, li, folder)
        if extra:
            out.extend(extra)
        self.add(out)
        if not out:
            self.add([self.notice(common.t("nothing_here"))])
        self.end(True, listing.content_for(es), cache=False)

    def notice(self, label):
        li = xbmcgui.ListItem(label=label, offscreen=True)
        li.setArt({"icon": "DefaultIconInfo.png"})
        return self.url("noop"), li, False

    def action(self, label, action, icon, **params):
        """A list entry that runs an action (sign in, settings) instead of
        opening a folder or playing."""
        url, li, _ = self.folder(label, action, icon, **params)
        return url, li, False

    def folder(self, label, action, icon="DefaultFolder.png", **params):
        url = self.url(action, **params)
        li = xbmcgui.ListItem(label=label, offscreen=True)
        li.setArt({"icon": icon, "thumb": icon})
        return url, li, True

    # ── menus ──

    def root(self):
        if not common.server_url() or not self.client.signed_in():
            self.add(
                [
                    self.action(common.t("sign_in"), "signin", "DefaultUser.png"),
                    self.action(common.t("settings"), "settings", "DefaultAddonService.png"),
                ]
            )
            return self.end(True, cache=False)
        c = self.client
        items = []
        if self.section == "image":
            for lib in listing.libraries_for(c.libraries(), "image"):
                items.append(self.folder(lib.get("name") or "", "photos", "DefaultPicture.png", id=lib["id"]))
            items.append(self.action(common.t("settings"), "settings", "DefaultAddonService.png"))
            self.add(items)
            return self.end(True, cache=False)
        hub = c.hub()
        for key, _rows in listing.watchable_rows(hub, self.section):
            if key not in ROW_LABELS:
                continue
            items.append(self.folder(common.t(ROW_LABELS[key]), "row", "DefaultRecentlyAddedEpisodes.png", row=key))
        for lib in listing.libraries_for(c.libraries(), self.section):
            icon = {
                "movie": "DefaultMovies.png",
                "show": "DefaultTVShows.png",
                "anime": "DefaultTVShows.png",
                "cartoons": "DefaultTVShows.png",
                "music": "DefaultMusicArtists.png",
                "audiobook": "DefaultMusicAlbums.png",
                "podcast": "DefaultMusicAlbums.png",
            }.get(lib.get("type"), "DefaultFolder.png")
            items.append(self.folder(lib.get("name") or "", "library", icon, id=lib["id"], kind=lib.get("type", "")))
        if self.section != "audio" and c.capabilities().get("live_tv"):
            items.append(self.folder(common.t("live_tv"), "livetv", "DefaultAddonPVRClient.png"))
        if hub.get("collection_rows") and self.section != "audio":
            items.append(self.folder(common.t("collections"), "collections", "DefaultSets.png"))
        if self.section != "audio":
            items.append(self.folder(common.t("watchlist"), "watchlist", "DefaultPlaylist.png"))
        items.append(self.folder(common.t("favorites"), "favorites", "DefaultFavourites.png"))
        # An action, not a folder: the keyboard runs outside a directory
        # request, so the Container.Update to the results navigates (from a
        # folder that then fails, Kodi 21 drops it).
        items.append(self.action(common.t("search"), "search", "DefaultAddonsSearch.png"))
        url, li, _ = self.action(common.t("settings"), "settings", "DefaultAddonService.png")
        li.setLabel2(common.t("signed_in_as", user=c.username()))
        items.append((url, li, False))
        self.add(items)
        self.end(True, cache=False)

    def do_row(self):
        hub = self.client.hub()
        key = self.params.get("row", "")
        for k, items in listing.watchable_rows(hub, self.section):
            if k == key:
                return self.list_entries(items, "continue" if key == "continue_watching" else "")
        self.list_entries([])

    def do_library(self):
        """A library's menu: everything, recently added, unwatched, and for
        music / audiobooks the second level (albums, books)."""
        lib_id = self.params.get("id", "")
        kind = self.params.get("kind", "")
        items = [self.folder(common.t("all_items"), "items", id=lib_id, kind=kind)]
        if kind in ("movie", "show", "anime", "cartoons", "home_video"):
            items.append(
                self.folder(
                    common.t("recently_added_lib"), "items", id=lib_id, kind=kind, sort="created_at", dir="desc"
                )
            )
            items.append(self.folder(common.t("unwatched"), "items", id=lib_id, kind=kind, watch="unwatched"))
            if kind == "movie":
                items.append(self.folder(common.t("in_progress"), "items", id=lib_id, kind=kind, watch="in_progress"))
        elif kind == "music":
            items.append(self.folder(common.t("albums"), "items", id=lib_id, kind=kind, type="album"))
            items.append(
                self.folder(
                    common.t("recently_added_lib"),
                    "items",
                    id=lib_id,
                    kind=kind,
                    type="album",
                    sort="created_at",
                    dir="desc",
                )
            )
        elif kind == "audiobook":
            items.append(self.folder(common.t("books"), "items", id=lib_id, kind=kind, type="audiobook"))
        elif kind == "photo":
            return self.do_photos()
        else:
            return self.do_items()
        self.add(items)
        self.end(True)

    def do_items(self):
        lib_id = self.params.get("id", "")
        try:
            offset = max(0, int(self.params.get("offset", "0")))
        except ValueError:
            offset = 0
        size = min(200, max(25, common.setting_int("page_size") or 200))
        sort = self.params.get("sort", "title")
        sort_dir = self.params.get("dir", "asc")
        items, total = self.client.library_items(
            lib_id,
            offset=offset,
            limit=size,
            sort=sort,
            sort_dir=sort_dir,
            watch=self.params.get("watch"),
            item_type=self.params.get("type"),
        )
        extra = []
        if total is not None and offset + len(items) < total:
            params = dict(self.params)
            params.pop("action", None)
            params["offset"] = str(offset + len(items))
            label = common.t("next_page", shown=offset + len(items), total=total)
            extra.append(self.folder(label, "items", "DefaultFolder.png", **params))
        self.list_entries(items, extra=extra)

    def do_children(self):
        item_id = self.params.get("id", "")
        kids = self.client.children(item_id)
        if not kids:
            # A single-file audiobook (or an empty folder): play the item.
            item = self.client.item(item_id)
            if item.get("files"):
                return self.list_entries(
                    [dict(item, type="audiobook_chapter" if item.get("type") == "audiobook" else item.get("type"))]
                )
        parent = {"show": self.params.get("show", "")}
        if self.params.get("season", "").isdigit():
            parent["season"] = int(self.params["season"])
        self.list_entries(kids, parent=parent)

    def do_collections(self):
        rows = self.client.hub().get("collection_rows") or []
        self.add(
            [
                self.folder(r.get("name") or "", "collection", "DefaultSets.png", id=r.get("collection_id", ""))
                for r in rows
            ]
        )
        self.end(True)

    def do_collection(self):
        self.list_entries(self.client.collection_items(self.params.get("id", "")))

    def do_watchlist(self):
        self.list_entries(self.client.watchlist("plan_to_watch"), "watchlist")

    def do_favorites(self):
        items = self.client.favorites()
        if self.section == "audio":
            items = [i for i in items if isinstance(i, dict) and i.get("type") in listing.MUSIC_TYPES]
        elif self.section == "video":
            items = [i for i in items if isinstance(i, dict) and i.get("type") not in listing.MUSIC_TYPES]
        self.list_entries(items, "favorites")

    def do_search(self):
        q = self.params.get("q")
        if not q:
            q = xbmcgui.Dialog().input(common.t("search"))
            if self.handle >= 0:
                self.end(False)
            if q:
                # The results under their own URL: going back from one
                # returns to them without asking again.
                xbmc.executebuiltin("Container.Update(%s)" % self.url("search", q=q))
            return
        self.list_entries(self.client.search(q))

    def do_noop(self):
        if self.handle >= 0:
            self.end(False)

    # ── photos ──

    def page(self):
        try:
            offset = max(0, int(self.params.get("offset", "0")))
        except ValueError:
            offset = 0
        return offset, min(200, max(25, common.setting_int("page_size") or 200))

    def next_page(self, action, offset, shown, total):
        if total is None or offset + shown >= total:
            return []
        params = dict(self.params)
        params.pop("action", None)
        params["offset"] = str(offset + shown)
        label = common.t("next_page", shown=offset + shown, total=total)
        return [self.folder(label, action, "DefaultFolder.png", **params)]

    def do_photos(self):
        """A photo library, newest first. Each entry is the image itself
        (through the artwork proxy), so Kodi's picture viewer and slideshow
        open it."""
        offset, size = self.page()
        items, total = self.client.library_items(
            self.params.get("id", ""), offset=offset, limit=size, sort="taken_at", sort_dir="desc", item_type="photo"
        )
        art = self.art()
        out = []
        for it in items:
            if not isinstance(it, dict) or it.get("type") != "photo" or not it.get("id"):
                continue
            li = xbmcgui.ListItem(label=it.get("title") or "", offscreen=True)
            thumb = art.photo_url(it["id"], 480)
            li.setArt({"thumb": thumb, "icon": thumb})
            li.setMimeType("image/jpeg")
            li.setContentLookup(False)
            out.append((art.photo_url(it["id"], 3840), li, False))
        out.extend(self.next_page("photos", offset, len(items), total))
        self.add(out or [self.notice(common.t("nothing_here"))])
        self.end(True, "images", cache=False)

    # ── Live TV ──

    def do_livetv(self):
        self.add(
            [
                self.folder(common.t("channels"), "channels", "DefaultTVShows.png"),
                self.folder(common.t("recordings"), "recordings", "DefaultFolder.png"),
            ]
        )
        self.end(True)

    def do_channels(self):
        c = self.client
        channels = c.channels()
        try:
            now_next = c.now_next()
        except api.ApiError:
            now_next = []  # no guide data: the channels still play
        self.add_entries(listing.channel_entries(channels, now_next, common.t), "videos")

    def do_recordings(self):
        self.add_entries(listing.recording_entries(self.client.recordings(), common.t), "videos")

    def add_entries(self, es, content):
        art = self.art()
        out = [listitems.from_entry(e, art) for e in es]
        if self.section:
            out = [(u + "&s=" + self.section, li, f) for u, li, f in out]
        self.add(out or [self.notice(common.t("nothing_here"))])
        self.end(True, content if out else "", cache=False)

    def do_channel(self):
        """Resolve a live channel: the server's live HLS for it, with the
        24 h asset token (the segment requests carry it too)."""
        c = self.client
        token = c.store.load().get("asset_token", "")
        url = playback.with_token(
            c.server_url
            + "/api/v1/tv/channels/%s/stream.m3u8" % urllib.parse.quote(self.params.get("id", ""), safe=""),
            token,
        )
        li = xbmcgui.ListItem(path=url, offscreen=True)
        li.setMimeType("application/vnd.apple.mpegurl")
        li.setContentLookup(False)
        common.log("live channel %s" % self.params.get("id"))
        xbmcplugin.setResolvedUrl(self.handle, True, li)

    # ── playback ──

    def do_play(self):
        player.resolve(self.handle, self.params, self.resume)

    # ── actions (RunPlugin) ──

    def do_signin(self):
        if auth.sign_in(force=True):
            xbmc.executebuiltin("Container.Refresh")

    def do_signout(self):
        auth.sign_out()
        xbmc.executebuiltin("Container.Refresh")

    def do_server(self):
        if auth.ask_server(common.server_url()):
            xbmc.executebuiltin("Container.Refresh")

    def do_settings(self):
        common.addon().openSettings()

    def do_watched(self):
        self.client.set_watched(self.params["id"], self.params.get("on") == "1")
        xbmc.executebuiltin("Container.Refresh")

    def do_favorite(self):
        self.client.set_favorite(self.params["id"], self.params.get("on") == "1")
        common.notify(common.t("done"), ms=1500)
        if self.params.get("on") != "1":
            xbmc.executebuiltin("Container.Refresh")

    def do_watchlist_set(self):
        self.client.set_watch_status(self.params["id"], self.params.get("status") or None)
        common.notify(common.t("done"), ms=1500)
        if not self.params.get("status"):
            xbmc.executebuiltin("Container.Refresh")

    def do_dismiss(self):
        self.client.dismiss_continue_watching(self.params["id"])
        xbmc.executebuiltin("Container.Refresh")


def run(argv=None):
    Router(argv or sys.argv).run()
