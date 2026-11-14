"""Stand-in for xbmcaddon: settings from a dict, strings from the add-on's
English .po file, the profile folder from the PROFILE global."""

import os
import re

HERE = os.path.dirname(os.path.abspath(__file__))
PO = os.path.join(
    HERE, "..", "..", "plugin.video.onscreen", "resources", "language", "resource.language.en_gb", "strings.po"
)

PROFILE = ""
settings = {}
DEFAULTS = {
    "server_url": "",
    "max_resolution": "2160",
    "hevc": True,
    "av1": True,
    "vp9": True,
    "hdr_mode": 0,
    "dovi_mode": 0,
    "ask_version": False,
    "page_size": 200,
    "art_proxy": True,
    "art_port": 52872,
    "receive_transfers": True,
    "offer_adaptive": True,
}
opened_settings = []


def reset():
    settings.clear()
    settings.update(DEFAULTS)
    opened_settings.clear()


reset()


def _strings():
    out = {}
    with open(PO, encoding="utf-8") as f:
        text = f.read()
    for m in re.finditer(r'msgctxt "#(\d+)"\nmsgid "((?:[^"\\]|\\.)*)"', text):
        out[int(m.group(1))] = m.group(2).replace('\\"', '"').replace("\\\\", "\\")
    return out


STRINGS = _strings()


class Addon:
    def __init__(self, addon_id=None):
        self.id = addon_id

    def getSetting(self, k):  # noqa: N802
        v = settings.get(k, "")
        if isinstance(v, bool):
            return "true" if v else "false"
        return str(v)

    def getSettingBool(self, k):  # noqa: N802
        return bool(settings.get(k, False))

    def getSettingInt(self, k):  # noqa: N802
        return int(settings.get(k, 0) or 0)

    def setSetting(self, k, v):  # noqa: N802
        settings[k] = v

    def setSettingBool(self, k, v):  # noqa: N802
        settings[k] = bool(v)

    def getLocalizedString(self, sid):  # noqa: N802
        return STRINGS.get(sid, "")

    def getAddonInfo(self, key):  # noqa: N802
        return {"profile": PROFILE, "version": "0.1.0", "id": "plugin.video.onscreen", "name": "OnScreen"}.get(key, "")

    def openSettings(self):  # noqa: N802
        opened_settings.append(True)
