"""What this Kodi plays, for the X-Client-Capabilities header.

Resolution and the HEVC / AV1 / VP9 switches come from the add-on settings,
since only the person knows whether a Raspberry Pi keeps up with 4K AV1 in
software. HDR and Dolby Vision follow the display by default: Kodi 20+
reports what the screen takes in System.SupportedHDRTypes. Audio
passthrough follows Kodi's own audio settings (read over JSON-RPC).
"""

import json

import xbmc
import xbmcaddon

from resources.lib.onscreen import caps as capsmod

HDR_AUTO, HDR_ON, HDR_OFF = 0, 1, 2
DOVI_AUTO, DOVI_OFF = 0, 1


def _addon():
    return xbmcaddon.Addon("plugin.video.onscreen")


def kodi_setting(name):
    """A Kodi setting's value over JSON-RPC, or None."""
    req = {"jsonrpc": "2.0", "id": 1, "method": "Settings.GetSettingValue", "params": {"setting": name}}
    try:
        resp = json.loads(xbmc.executeJSONRPC(json.dumps(req)))
    except (ValueError, TypeError):
        return None
    return (resp.get("result") or {}).get("value")


def passthrough_codecs():
    """The codecs Kodi bitstreams to the receiver: AC-3 / E-AC-3 when
    passthrough is on and they're enabled. (Only these two go into the
    header; see onscreen/caps.py.)"""
    if not kodi_setting("audiooutput.passthrough"):
        return ()
    out = []
    if kodi_setting("audiooutput.ac3passthrough"):
        out.append("ac3")
    if kodi_setting("audiooutput.eac3passthrough"):
        out.append("eac3")
    return tuple(out)


def caps():
    a = _addon()
    width, height = capsmod.RESOLUTIONS.get(a.getSetting("max_resolution") or "2160", capsmod.RESOLUTIONS["2160"])
    hdr_types = xbmc.getInfoLabel("System.SupportedHDRTypes")
    hdr10, hlg, dovi = capsmod.parse_hdr_types(hdr_types)
    mode = a.getSettingInt("hdr_mode")
    if mode == HDR_ON:
        hdr10, hlg = True, True
    elif mode == HDR_OFF:
        hdr10, hlg = False, False
    if a.getSettingInt("dovi_mode") == DOVI_OFF:
        dovi = False
    return capsmod.DeviceCaps(
        max_width=width,
        max_height=height,
        hevc=a.getSettingBool("hevc"),
        av1=a.getSettingBool("av1"),
        vp9=a.getSettingBool("vp9"),
        hdr10=hdr10,
        hlg=hlg,
        dovi=capsmod.DOVI_PROFILES if dovi else (),
        passthrough=passthrough_codecs(),
    )
