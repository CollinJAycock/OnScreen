"""Choosing a server and signing in.

Pairing is the default, as on every OnScreen TV app: the add-on shows a
six-digit PIN, the person enters it at <server>/pair on a phone or computer
(where they're signed in, SSO included), and the add-on's poll receives a
token pair. Username + password (and a two-factor code) is the fallback.
"""

import time

import xbmc
import xbmcgui

from resources.lib.kodi import common
from resources.lib.onscreen import api


def ensure_server():
    """The configured server URL, asking for one when none is set (or the
    one set is plain http:// to a host outside the local network, which
    the client refuses). "" when the person cancels."""
    current = common.server_url()
    if current and api.cleartext_remote(current):
        xbmcgui.Dialog().ok(
            "OnScreen", address_problem(api.ServerAddressError("cleartext_remote", api.url_host(current)))
        )
        return ask_server(current)
    if current:
        return current
    return ask_server()


def address_problem(err):
    """The sentence for an api.ServerAddressError."""
    if err.kind == "cleartext_remote":
        return common.t("server_cleartext", host=err.host)
    return common.t("server_invalid")


def ask_server(default=""):
    """Ask for the server address until it answers like an OnScreen server.
    Without a scheme the address is tried over https:// first, and over
    http:// only for a server on the local network; plain http:// to any
    other host is refused (api.server_url_candidates). Saves and returns the
    URL that answered; "" on cancel."""
    value = default
    while True:
        value = xbmcgui.Dialog().input(common.t("enter_server"), value)
        if not value or value.strip() in ("http://", "https://"):
            return ""
        try:
            candidates = api.server_url_candidates(value)
        except api.ServerAddressError as e:
            xbmcgui.Dialog().ok("OnScreen", address_problem(e))
            continue
        for url in candidates:
            resp = common.client(url).check_server()
            if resp.ok and isinstance(resp.data, dict):
                common.addon().setSetting("server_url", url)
                return url
            if not resp.network_error:
                break  # an answer, just not OnScreen's: no plain-http retry
        if resp.network_error:
            xbmcgui.Dialog().ok("OnScreen", common.t("server_unreachable", url=url, reason=resp.error_message))
        else:
            xbmcgui.Dialog().ok("OnScreen", common.t("not_onscreen", url=url))


def sign_in(force=False):
    """Run a sign-in. True when signed in afterwards. force: sign in again
    even with a stored sign-in (the settings button)."""
    server = ensure_server()
    if not server:
        return False
    c = common.client(server)
    if c.signed_in():
        if not force:
            return True
        c.logout()  # switching accounts: revoke this device's old sign-in first
    choice = xbmcgui.Dialog().select(common.t("sign_in_how"), [common.t("sign_in_pair"), common.t("sign_in_password")])
    if choice == 0:
        ok = pair(c, server)
    elif choice == 1:
        ok = password(c)
    else:
        return False
    if ok:
        common.notify(common.t("signed_in", user=c.username()))
    return ok


def pair(c, server):
    """The PIN flow. A code lives 10 minutes; an expired one is replaced
    (code creation is rate-limited, so never faster than that)."""
    progress = xbmcgui.DialogProgress()
    monitor = xbmc.Monitor()
    progress.create(common.t("pair_title"), common.t("starting"))
    try:
        while True:
            resp = c.pair_code()
            if not resp.ok or not isinstance(resp.data, dict) or not resp.data.get("device_token"):
                progress.close()
                xbmcgui.Dialog().ok(
                    "OnScreen", common.t("pair_failed", reason=resp.error_message or "HTTP %d" % resp.status)
                )
                return False
            pin = str(resp.data.get("pin") or "")
            device_token = resp.data["device_token"]
            poll_every = resp.data.get("poll_after") or 2
            poll_every = min(3, max(1, poll_every if isinstance(poll_every, int) else 2))
            ttl = 600
            started = time.monotonic()
            text = "%s[CR][B]%s/pair[/B][CR][CR]%s [B]%s[/B]" % (
                common.t("pair_open"),
                server,
                common.t("pair_pin"),
                " ".join(pin) if len(pin) <= 8 else pin,
            )
            while True:
                elapsed = time.monotonic() - started
                left = max(0, ttl - elapsed)
                progress.update(int(100 * left / ttl), text)
                if progress.iscanceled() or monitor.waitForAbort(poll_every):
                    return False
                poll = c.pair_poll(device_token)
                if poll.status == 200 and c.signed_in():
                    return True
                if poll.status == 410 or left <= 0:
                    progress.update(0, common.t("pair_expired"))
                    break  # new code
                # 202 (waiting) or a network blip: keep polling.
    finally:
        progress.close()


def password(c):
    username = xbmcgui.Dialog().input(common.t("username"))
    if not username:
        return False
    pw = xbmcgui.Dialog().input(common.t("password"), option=xbmcgui.ALPHANUM_HIDE_INPUT)
    if not pw:
        return False
    resp = c.login(username, pw)
    if resp.ok and isinstance(resp.data, dict) and resp.data.get("totp_required"):
        code = xbmcgui.Dialog().input(common.t("totp_code"))
        if not code:
            return False
        resp = c.totp_verify(resp.data.get("login_challenge_token") or "", code.strip())
    if c.signed_in():
        return True
    xbmcgui.Dialog().ok("OnScreen", common.t("login_failed", reason=resp.error_message or "HTTP %d" % resp.status))
    return False


def sign_out():
    c = common.client()
    if not c.signed_in():
        return
    if not xbmcgui.Dialog().yesno("OnScreen", common.t("sign_out_confirm", user=c.username())):
        return
    c.logout()
    common.notify(common.t("signed_out"))
