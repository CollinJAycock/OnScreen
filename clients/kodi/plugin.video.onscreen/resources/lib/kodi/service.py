"""The add-on's background service (xbmc.service, started at login).

Two jobs:
- the artwork proxy (onscreen/artproxy.py), so list artwork has stable URLs;
- following playback the plugin resolved: a progress report every 10 s
  while playing, one on pause / resume / seek, a final "stopped" (which is
  what moves Continue Watching and marks an item played past 90 %), and
  deleting the remux / transcode session afterwards so its ffmpeg stops.

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

from resources.lib.kodi import common
from resources.lib.onscreen import artproxy, playback

HEARTBEAT_SEC = 10
DEFAULT_ART_PORT = 52872


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
        elif kind == "refresh":
            if xbmc.getInfoLabel("Container.FolderPath").startswith("plugin://%s/" % common.ADDON_ID):
                xbmc.executebuiltin("Container.Refresh")

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
