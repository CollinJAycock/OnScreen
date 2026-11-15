"""The add-on's background service (xbmc.service, started at login).

Its jobs:
- the artwork proxy (onscreen/artproxy.py), so list artwork has stable URLs;
- following playback the plugin resolved: a progress report every 10 s
  while playing, one on pause / resume / seek, a final "stopped" (which is
  what moves Continue Watching and marks an item played past 90 %), and
  deleting the remux / transcode session afterwards so its ffmpeg stops;
- switching to the audio / subtitle track the plugin picked once Kodi's
  player is up, and handing the next episode to the Up Next add-on;
- receiving "Play on <this Kodi>" from the other OnScreen apps: it asks the
  server for a waiting transfer every few seconds, as the Roku does, since
  Kodi can't hold the server's event stream open for it either.

The plugin leaves what it resolved in Window(10000) property
onscreen.playback; the service adopts it when Kodi's player starts that URL.
A heartbeat the server answers with 403 PLAYBACK_STOPPED (an admin stopped
the stream) or PARENTAL_LIMIT stops the player and says why.
"""

import json
import queue
import threading
import time

import xbmc
import xbmcgui

from resources.lib.kodi import common, listitems
from resources.lib.onscreen import api, artproxy, playback, upnext

HEARTBEAT_SEC = 10
DEFAULT_ART_PORT = 52872
# How often to ask for a "Play on…" transfer: every few seconds while Kodi
# is in use, less often once it has sat idle for 10 minutes.
TRANSFER_POLL_SEC = 5
TRANSFER_IDLE_POLL_SEC = 30


def _same_stream(resolved, playing):
    if not resolved or not playing:
        return False
    return playing.split("|", 1)[0] == resolved.split("|", 1)[0]


class Tracker(xbmc.Player):
    def __init__(self, service):
        super().__init__()
        self.service = service

    def onAVStarted(self):  # noqa: N802 - Kodi's callback names
        self.service.adopt()

    def onPlayBackPaused(self):  # noqa: N802
        self.service.report_now("paused")

    def onPlayBackResumed(self):  # noqa: N802
        self.service.report_now("playing")

    def onPlayBackSeek(self, time_ms, offset_ms):  # noqa: N802
        self.service.report_now("playing")

    def onPlayBackStopped(self):  # noqa: N802
        self.service.finish(ended=False)

    def onPlayBackEnded(self):  # noqa: N802
        self.service.finish(ended=True)

    def onPlayBackError(self):  # noqa: N802
        self.service.finish(ended=False)


class Service:
    def __init__(self):
        self.monitor = xbmc.Monitor()
        self.window = xbmcgui.Window(10000)
        self.lock = threading.Lock()
        self.current = None
        self.jobs = queue.Queue()
        self.player = Tracker(self)
        self.proxy = None
        self._client = None
        self._client_at = 0.0
        self._worker = threading.Thread(target=self._work, name="onscreen-reports", daemon=True)
        self._poller = threading.Thread(target=self._poll_transfers, name="onscreen-transfers", daemon=True)

    def client(self):
        """An API client, rebuilt each minute (it carries the server URL and
        the capability header, which follow the settings). The tokens are
        read from the token file on every request either way."""
        with self.lock:
            if self._client is None or time.monotonic() - self._client_at > 60:
                self._client = common.client()
                self._client_at = time.monotonic()
            return self._client

    # ── lifecycle ──

    def run(self):
        common.log("service started")
        self._worker.start()
        self._poller.start()
        self.start_proxy()
        try:
            while not self.monitor.abortRequested():
                if self.monitor.waitForAbort(1):
                    break
                self.tick()
        finally:
            self.finish(ended=False)
            self.jobs.put(None)
            self._worker.join(5)
            if self.proxy:
                self.proxy.stop()
            self.window.clearProperty(common.PROP_ART_PORT)
            common.log("service stopped")

    def start_proxy(self):
        if not common.setting_bool("art_proxy"):
            self.window.clearProperty(common.PROP_ART_PORT)
            return

        def fetch(path):
            c = self.client()
            if not c.server_url or not c.signed_in():
                return 401, "text/plain", b""
            return c.fetch_artwork(path)

        self.proxy = artproxy.ArtProxy(fetch, log=lambda m: common.log(m, xbmc.LOGWARNING))
        port = self.proxy.start(common.setting_int("art_port") or DEFAULT_ART_PORT)
        if port:
            self.window.setProperty(common.PROP_ART_PORT, str(port))
            common.log("artwork proxy on 127.0.0.1:%d" % port)
        else:
            self.proxy = None
            self.window.clearProperty(common.PROP_ART_PORT)
            common.log("artwork proxy: no free port", xbmc.LOGWARNING)

    # ── playback ──

    def adopt(self):
        """The player started something: if it's the stream the plugin just
        resolved, follow it."""
        raw = self.window.getProperty(common.PROP_PLAYBACK)
        try:
            playing = self.player.getPlayingFile()
        except RuntimeError:
            return
        # Kodi reuses its player when another file starts straight away and
        # sends no onPlayBackStopped, so an item still tracked that isn't
        # what plays now has stopped: close it first. Otherwise its position
        # followed the other video's clock (another add-on's video, a local
        # file, OnScreen Live TV) and the end of that one could mark it
        # watched.
        with self.lock:
            cur = self.current
        if cur is not None and not _same_stream(cur.get("url"), playing):
            self.finish(ended=False)
        if not raw:
            return
        try:
            data = json.loads(raw)
        except ValueError:
            self.window.clearProperty(common.PROP_PLAYBACK)
            return
        if not _same_stream(data.get("url"), playing):
            return
        self.window.clearProperty(common.PROP_PLAYBACK)
        self.finish(ended=False)  # a playlist moving on: close the last one
        data["last_ms"] = self._position(data)
        data["reported_at"] = 0.0
        with self.lock:
            self.current = data
        self.report_now("playing")
        self.apply_selection(data.get("select"))
        item = data.get("item") or {}
        if item.get("type") == "episode" and xbmc.getCondVisibility("System.AddonIsEnabled(service.upnext)"):
            self.jobs.put(("upnext", data))

    def apply_selection(self, select):
        """Switch Kodi's player to the tracks the plugin picked (positions
        in Kodi's own audio / subtitle lists, see player.selection)."""
        if not isinstance(select, dict):
            return
        try:
            if isinstance(select.get("audio"), int):
                self.player.setAudioStream(select["audio"])
            if isinstance(select.get("subtitle"), int):
                self.player.setSubtitleStream(select["subtitle"])
                self.player.showSubtitles(True)
            elif select.get("subtitles_off"):
                self.player.showSubtitles(False)
        except RuntimeError as e:  # stopped already
            common.log("track switch: %s" % e, xbmc.LOGWARNING)

    def _position(self, cur):
        try:
            sec = self.player.getTime()
        except RuntimeError:
            return cur.get("last_ms", 0)
        return int(cur.get("offset_ms") or 0) + int(round(max(0.0, sec) * 1000))

    def tick(self):
        with self.lock:
            cur = self.current
        if cur is None or not self.player.isPlaying():
            return
        cur["last_ms"] = self._position(cur)
        if xbmc.getCondVisibility("Player.Paused"):
            return
        if time.monotonic() - cur["reported_at"] >= HEARTBEAT_SEC:
            self._report(cur, "playing")

    def report_now(self, state):
        with self.lock:
            cur = self.current
        if cur is None:
            return
        cur["last_ms"] = self._position(cur)
        self._report(cur, state)

    def _report(self, cur, state):
        cur["reported_at"] = time.monotonic()
        self.jobs.put(("progress", cur, state, cur["last_ms"]))

    def finish(self, ended):
        with self.lock:
            cur = self.current
            self.current = None
        if cur is None:
            return
        duration = cur.get("duration_ms") or 0
        pos = cur.get("last_ms") or 0
        # The end of the stream counts as the end of the item only when the
        # playhead was near it: a session that dropped early mustn't mark
        # the item played.
        if ended and duration and pos >= min(duration * 0.9, duration - 60000):
            pos = duration
        self.jobs.put(("progress", cur, "stopped", pos))
        session = cur.get("session")
        if session and session.get("id"):
            self.jobs.put(("stop_session", session))
        self.jobs.put(("refresh",))

    # ── the report thread ──

    def _work(self):
        while True:
            job = self.jobs.get()
            if job is None:
                return
            try:
                self._do(job)
            except Exception as e:  # noqa: BLE001 - keep the thread alive
                common.log("report: %s" % e, xbmc.LOGWARNING)

    def _do(self, job):
        kind = job[0]
        if kind == "progress":
            _, cur, state, pos = job
            body = {
                "view_offset_ms": int(pos),
                "duration_ms": int(cur.get("duration_ms") or 0),
                "state": state,
                "client_name": common.device_name(),
                "decision": cur.get("decision") or "",
                "file_id": cur.get("file_id") or "",
            }
            resp = self.client().report_progress(cur["item_id"], body)
            if state != "stopped":
                self._check_refusal(cur, resp)
        elif kind == "stop_session":
            session = job[1]
            self.client().transcode_stop(session["id"], session.get("token") or "")
        elif kind == "upnext":
            self._send_upnext(job[1])
        elif kind == "refresh":
            if xbmc.getInfoLabel("Container.FolderPath").startswith("plugin://%s/" % common.ADDON_ID):
                xbmc.executebuiltin("Container.Refresh")

    def _send_upnext(self, cur):
        """Hand Up Next the current and next episode (after a lookup that
        takes a few requests, so on this thread)."""
        c = self.client()
        item = dict(cur.get("item") or {}, id=cur.get("item_id"))
        try:
            nxt, next_season = upnext.find_next(c, item)
            if nxt is None:
                return
            season = c.item(item["parent_id"])
            show_id = item.get("grandparent_id") or season.get("parent_id")
            show = c.item(show_id) if show_id else {}
            current = c.item(item["id"])
        except api.ApiError as e:
            common.log("up next: %s" % e, xbmc.LOGWARNING)
            return
        with self.lock:
            if self.current is not cur:
                return  # playback moved on meanwhile
        art = listitems.ArtResolver(c.server_url, c.store.load().get("asset_token", ""))
        req = upnext.notification(
            common.ADDON_ID,
            upnext.episode_info(current, show, season.get("index"), art.url),
            upnext.episode_info(nxt, show, next_season, art.url),
            listitems.plugin_url("play", id=nxt["id"], s="video"),
        )
        xbmc.executeJSONRPC(json.dumps(req))
        common.log("up next: %s after %s" % (nxt.get("id"), item.get("id")))

    # ── "Play on…" ──

    def _poll_transfers(self):
        while not self.monitor.abortRequested():
            idle = xbmc.getGlobalIdleTime() > 600
            if self.monitor.waitForAbort(TRANSFER_IDLE_POLL_SEC if idle else TRANSFER_POLL_SEC):
                return
            if not common.setting_bool("receive_transfers"):
                continue
            try:
                self.claim_transfer()
            except Exception as e:  # noqa: BLE001 - keep polling
                common.log("transfer poll: %s" % e, xbmc.LOGWARNING)

    def claim_transfer(self):
        """Start a transfer the server holds for this Kodi, if any."""
        c = self.client()
        if not c.server_url or not c.signed_in():
            return
        resp = c.claim_transfer(common.device_name())
        if resp.status != 200 or not isinstance(resp.data, dict) or not resp.data.get("item_id"):
            return
        start = resp.data.get("position_ms")
        params = {"id": resp.data["item_id"]}
        if isinstance(start, int) and start > 0:
            params["start"] = str(start)
        common.log("play-on transfer: %s at %s ms" % (params["id"], start))
        xbmc.executebuiltin("PlayMedia(%s)" % listitems.plugin_url("play", **params))

    def _check_refusal(self, cur, resp):
        plan = playback.refusal(resp)
        if plan is None:
            return
        with self.lock:
            still = self.current is cur
        if not still:
            return
        from resources.lib.kodi import player as plugin_player  # the sentences

        message = plugin_player.refusal_text(plan)
        common.log("server stopped playback: %s" % plan.code)
        self.player.stop()
        title = common.t("watch_limit") if plan.code == "PARENTAL_LIMIT" else "OnScreen"
        xbmcgui.Dialog().ok(title, message)


def run():
    Service().run()
