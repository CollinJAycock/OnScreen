"""Shared by the plugin and the service: settings, strings, logging, the
token file and the API client."""

import os
import platform

import xbmc
import xbmcaddon
import xbmcgui
import xbmcvfs

from resources.lib.kodi import device
from resources.lib.onscreen import api, caps

ADDON_ID = "plugin.video.onscreen"

# Window(10000) properties the plugin and the service share (they run in
# separate interpreters).
PROP_PLAYBACK = "onscreen.playback"  # JSON: the stream the plugin just resolved
PROP_ART_PORT = "onscreen.artport"  # the artwork proxy's port, when it runs

# String ids (resources/language/*/strings.po). Keys are what the pure
# modules pass to t().
STRINGS = {
    "server_address": 30001,
    "sign_in": 30003,
    "sign_out": 30004,
    "continue_watching": 30100,
    "next_up": 30101,
    "recently_added": 30102,
    "plan_to_watch": 30103,
    "trending": 30104,
    "libraries": 30105,
    "watchlist": 30106,
    "favorites": 30107,
    "search": 30108,
    "settings": 30109,
    "next_page": 30110,
    "signed_in_as": 30111,
    "not_signed_in": 30112,
    "collections": 30113,
    "play": 30114,
    "all_items": 30115,
    "unwatched": 30116,
    "in_progress": 30117,
    "recently_added_lib": 30118,
    "albums": 30119,
    "books": 30126,
    "season_n": 30120,
    "specials": 30121,
    "episode_n": 30122,
    "version_n": 30123,
    "version_parts": 30124,
    "choose_version": 30125,
    "play_options": 30130,
    "mark_watched": 30131,
    "mark_unwatched": 30132,
    "add_watchlist": 30133,
    "remove_watchlist": 30134,
    "add_favorite": 30135,
    "remove_favorite": 30136,
    "remove_continue": 30137,
    "go_to_show": 30138,
    "resume_from": 30140,
    "play_beginning": 30141,
    "track_n": 30142,
    "subtitles_off": 30143,
    "forced": 30144,
    "burned_in": 30145,
    "external": 30146,
    "choose_audio": 30147,
    "choose_subtitles": 30148,
    "live_tv": 30150,
    "channels": 30151,
    "recordings": 30152,
    "guide_now": 30153,
    "guide_next": 30154,
    "recording_scheduled": 30155,
    "recording_recording": 30156,
    "recording_failed": 30157,
    "recording_cancelled": 30158,
    "recording_superseded": 30159,
    "enter_server": 30200,
    "server_unreachable": 30201,
    "not_onscreen": 30202,
    "pair_title": 30203,
    "pair_open": 30204,
    "pair_pin": 30205,
    "pair_expired": 30206,
    "pair_failed": 30207,
    "sign_in_how": 30208,
    "sign_in_pair": 30209,
    "sign_in_password": 30210,
    "username": 30211,
    "password": 30212,
    "totp_code": 30213,
    "login_failed": 30214,
    "signed_in": 30215,
    "signed_out": 30216,
    "sign_out_confirm": 30217,
    "session_expired": 30218,
    "too_many_streams": 30300,
    "too_many_streams_text": 30301,
    "admin_stopped": 30302,
    "admin_stopped_plain": 30303,
    "watch_limit": 30304,
    "parental_hours": 30305,
    "parental_daily": 30306,
    "parental_blocked": 30307,
    "dv_unsupported": 30308,
    "damaged": 30309,
    "playback_failed": 30310,
    "no_files": 30311,
    "starting": 30312,
    "error": 30313,
    "done": 30314,
    "nothing_here": 30315,
    "burn_unavailable": 30316,
    "burn_stacked": 30317,
    "burn_failed": 30318,
    "adaptive_why": 30319,
}


def addon():
    return xbmcaddon.Addon(ADDON_ID)


def t(key, **params):
    """The string for key in Kodi's language, with {name} placeholders
    filled. Unknown keys come back as the key (a bug, but not a crash)."""
    sid = STRINGS.get(key)
    s = addon().getLocalizedString(sid) if sid else ""
    if not s:
        s = key
    if params:
        try:
            s = s.format(**params)
        except (KeyError, IndexError, ValueError):
            pass
    return s


def log(msg, level=xbmc.LOGINFO):
    xbmc.log("[%s] %s" % (ADDON_ID, msg), level)


def notify(message, title="OnScreen", icon=xbmcgui.NOTIFICATION_INFO, ms=5000):
    xbmcgui.Dialog().notification(title, message, icon, ms)


def profile_dir():
    return xbmcvfs.translatePath(addon().getAddonInfo("profile"))


def token_store():
    return api.FileTokenStore(os.path.join(profile_dir(), "auth.json"))


def setting(key):
    return addon().getSetting(key)


def setting_bool(key):
    return addon().getSettingBool(key)


def setting_int(key):
    return addon().getSettingInt(key)


def server_url():
    return api.normalize_server_url(setting("server_url"))


def version():
    return addon().getAddonInfo("version")


def kodi_version():
    return xbmc.getInfoLabel("System.BuildVersion").split(" ")[0]


def kodi_major():
    try:
        return int(kodi_version().split(".")[0])
    except ValueError:
        return 0


def language():
    """Kodi's UI language as a BCP 47 tag the server's catalog knows:
    "es", "zh-Hans", else the two-letter code."""
    code = (xbmc.getLanguage(xbmc.ISO_639_1) or "en").lower()
    if code == "zh":
        return "zh-Hans"
    return code or "en"


def language_name(code):
    """A track's language code ("spa", "en") as Kodi names it ("Spanish"),
    or the code when Kodi doesn't know it."""
    try:
        return xbmc.convertLanguage(code, xbmc.ENGLISH_NAME) or code
    except (TypeError, ValueError, RuntimeError):
        return code


def device_name():
    """The name the server shows on its device list and Now Playing."""
    name = xbmc.getInfoLabel("System.FriendlyName") or ""
    if not name or name.lower() == "kodi":
        return "Kodi (%s)" % platform.node() if platform.node() else "Kodi"
    return name if "kodi" in name.lower() else "Kodi (%s)" % name


def user_agent():
    return "OnScreen-Kodi/%s Kodi/%s (%s)" % (version(), kodi_version(), platform.system() or "unknown")


def client(server=None):
    """An API client for the configured server with this device's headers."""
    headers = {
        "Accept-Language": language(),
        "User-Agent": user_agent(),
        "X-Device-Name": device_name(),
        "X-Client-Capabilities": caps.header(device.caps()),
    }
    return api.Client(server if server is not None else server_url(), token_store(), headers=headers)
