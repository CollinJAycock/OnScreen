"""A stand-in for Kodi's xbmc module: enough for the add-on's glue to run
under pytest. State lives in module globals the tests set and inspect."""

import json

LOGDEBUG, LOGINFO, LOGWARNING, LOGERROR = 0, 1, 2, 3
ISO_639_1 = 0
ENGLISH_NAME = 2

info_labels = {
    "System.BuildVersion": "21.1 (21.1.0) Git:stub",
    "System.FriendlyName": "Kodi",
    "System.SupportedHDRTypes": "",
}
conditions = {}
builtins = []
logs = []
language = "en"
jsonrpc_settings = {"audiooutput.passthrough": False}


def reset():
    builtins.clear()
    logs.clear()
    conditions.clear()


def log(msg, level=LOGDEBUG):
    logs.append((level, msg))


def getInfoLabel(name):  # noqa: N802 - Kodi's names
    return info_labels.get(name, "")


def getCondVisibility(cond):  # noqa: N802
    return bool(conditions.get(cond, False))


def getLanguage(fmt=None, region=False):  # noqa: N802
    return language


def executebuiltin(cmd, wait=False):
    builtins.append(cmd)


def executeJSONRPC(raw):  # noqa: N802
    req = json.loads(raw)
    if req.get("method") == "Settings.GetSettingValue":
        name = req["params"]["setting"]
        return json.dumps({"id": req.get("id"), "jsonrpc": "2.0", "result": {"value": jsonrpc_settings.get(name)}})
    return json.dumps({"id": req.get("id"), "jsonrpc": "2.0", "error": {"code": -32601}})


class Monitor:
    def abortRequested(self):  # noqa: N802
        return False

    def waitForAbort(self, timeout=0):  # noqa: N802
        return False


class Player:
    playing_file = ""
    time = 0.0
    is_playing = False

    def __init__(self):
        pass

    def getPlayingFile(self):  # noqa: N802
        if not self.is_playing:
            raise RuntimeError("not playing")
        return self.playing_file

    def getTime(self):  # noqa: N802
        if not self.is_playing:
            raise RuntimeError("not playing")
        return self.time

    def isPlaying(self):  # noqa: N802
        return self.is_playing

    def stop(self):
        self.is_playing = False
