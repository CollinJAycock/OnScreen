"""HTTP client for the OnScreen API (/api/v1).

Standard library only (urllib), so the add-on needs no script.module.*
dependency. Every response is a Response; the JSON envelope is unwrapped
({"data": ..., "meta": ...} and {"error": {"code", "message"}}).

Tokens. A sign-in (password or device pairing) gives an access token (about
an hour), a refresh token (30 days, rotated on every use) and an asset token
(24 h). Kodi runs the plugin and the add-on's service in separate Python
interpreters, so the tokens live in a file both read (FileTokenStore). The
server treats a refresh token presented twice as stolen and ends the whole
sign-in, so two interpreters must never refresh with the same token: the
refresh runs under a file lock and re-reads the stored token first. If
another interpreter already rotated it, this one just replays with the new
pair instead of spending the old one.
"""

import contextlib
import json
import os
import socket
import ssl
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

API = "/api/v1"

# Stored sign-in fields. server_url isn't one of them: it lives in the
# add-on settings, and signing out keeps it.
TOKEN_FIELDS = ("access_token", "refresh_token", "asset_token", "username", "user_id", "is_admin")


def normalize_server_url(value):
    """The server URL as the client joins paths to it: a scheme (http:// when
    none was typed), no trailing slash. A sub-path (https://host/onscreen) is
    kept. Returns "" for an empty value."""
    s = (value or "").strip()
    if not s:
        return ""
    if "://" not in s:
        s = "http://" + s
    return s.rstrip("/")


class Response:
    """One HTTP exchange. status is the HTTP status, or 0 when the request
    never got an answer (DNS, refused, timeout, TLS); error_message then says
    why. data / meta are the envelope's fields, error_code / error_message
    the error envelope's."""

    __slots__ = ("status", "raw", "content_type", "body", "data", "meta", "error_code", "error_message")

    def __init__(self, status, raw=b"", content_type=""):
        self.status = status
        self.raw = raw or b""
        self.content_type = content_type or ""
        self.body = None
        self.data = None
        self.meta = {}
        self.error_code = ""
        self.error_message = ""
        if self.raw and (not self.content_type or "json" in self.content_type):
            try:
                self.body = json.loads(self.raw.decode("utf-8"))
            except (ValueError, UnicodeDecodeError):
                self.body = None
        if isinstance(self.body, dict):
            self.data = self.body.get("data")
            meta = self.body.get("meta")
            self.meta = meta if isinstance(meta, dict) else {}
            err = self.body.get("error")
            if isinstance(err, dict):
                self.error_code = str(err.get("code") or "")
                self.error_message = str(err.get("message") or "").strip()

    @property
    def ok(self):
        return 200 <= self.status < 300

    @property
    def network_error(self):
        return self.status <= 0

    @property
    def total(self):
        """meta.total of a list response, or None."""
        t = self.meta.get("total")
        return t if isinstance(t, int) else None

    def __repr__(self):
        return "Response(%d, %s)" % (self.status, self.error_code or "")


class ApiError(Exception):
    """A request that didn't succeed: a non-2xx status or no answer."""

    def __init__(self, response):
        self.response = response
        self.status = response.status
        self.code = response.error_code
        self.message = response.error_message
        super().__init__(self.message or self.code or "HTTP %d" % self.status)

    @property
    def signed_out(self):
        return self.status == 401


# ── Token stores ────────────────────────────────────────────────────────────


class MemoryTokenStore:
    """Tokens in memory, for the tests and for a client that never persists."""

    def __init__(self, tokens=None):
        self._tokens = dict(tokens or {})
        self._lock = threading.RLock()

    def load(self):
        with self._lock:
            return dict(self._tokens)

    def save(self, tokens):
        with self._lock:
            self._tokens = {k: tokens[k] for k in TOKEN_FIELDS if tokens.get(k) not in (None, "")}

    def clear(self):
        with self._lock:
            self._tokens = {}

    @contextlib.contextmanager
    def lock(self):
        with self._lock:
            yield


class FileTokenStore:
    """Tokens in a JSON file (the add-on's profile folder), shared by every
    Kodi Python interpreter the add-on runs in.

    lock() is a cross-process lock: a lock file created with O_EXCL, which
    works the same on Windows, Linux and Android. A holder that died leaves
    the file behind; one older than `stale` seconds is taken over."""

    def __init__(self, path, lock_timeout=40.0, stale=60.0):
        self.path = path
        self.lock_path = path + ".lock"
        self.lock_timeout = lock_timeout
        self.stale = stale

    def load(self):
        try:
            with open(self.path, encoding="utf-8") as f:
                data = json.load(f)
        except (OSError, ValueError):
            return {}
        if not isinstance(data, dict):
            return {}
        return {k: data[k] for k in TOKEN_FIELDS if k in data}

    def save(self, tokens):
        data = {k: tokens[k] for k in TOKEN_FIELDS if tokens.get(k) not in (None, "")}
        folder = os.path.dirname(self.path)
        if folder:
            os.makedirs(folder, exist_ok=True)
        tmp = "%s.%d.tmp" % (self.path, os.getpid())
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(data, f)
        os.replace(tmp, self.path)

    def clear(self):
        try:
            os.remove(self.path)
        except OSError:
            pass

    @contextlib.contextmanager
    def lock(self):
        folder = os.path.dirname(self.lock_path)
        if folder:
            os.makedirs(folder, exist_ok=True)
        deadline = time.monotonic() + self.lock_timeout
        while True:
            try:
                fd = os.open(self.lock_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY)
            except (FileExistsError, PermissionError):
                # Windows answers PermissionError while the previous holder's
                # lock file is still being deleted: held, try again.
                try:
                    age = time.time() - os.path.getmtime(self.lock_path)
                except OSError:
                    continue  # released between the two calls
                if age > self.stale:
                    try:
                        os.remove(self.lock_path)
                    except OSError:
                        pass
                    continue
                if time.monotonic() > deadline:
                    raise TimeoutError("token lock held by another process") from None
                time.sleep(0.05)
                continue
            try:
                os.write(fd, str(os.getpid()).encode("ascii"))
            finally:
                os.close(fd)
            break
        try:
            yield
        finally:
            try:
                os.remove(self.lock_path)
            except OSError:
                pass


# ── HTTP ────────────────────────────────────────────────────────────────────


class _RedirectHandler(urllib.request.HTTPRedirectHandler):
    """urllib's redirect handler copies every header to the new request,
    Authorization included, even to another host. Drop it when the host
    changes (a proxy bouncing http to https on the same host keeps it)."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        new = super().redirect_request(req, fp, code, msg, headers, newurl)
        if new is not None:
            old_host = urllib.parse.urlsplit(req.full_url).hostname
            new_host = urllib.parse.urlsplit(newurl).hostname
            if old_host != new_host:
                new.remove_header("Authorization")
        return new


def default_opener():
    return urllib.request.build_opener(_RedirectHandler())


class Client:
    """The API. headers are added to every request: Accept-Language,
    X-Client-Capabilities, X-Device-Name and the User-Agent (the Kodi glue
    fills them in)."""

    def __init__(self, server_url, store, headers=None, timeout=20, opener=None):
        self.server_url = normalize_server_url(server_url)
        self.store = store
        self.headers = dict(headers or {})
        self.timeout = timeout
        self._opener = opener or default_opener()

    # ── plumbing ──

    def url(self, path, query=None):
        """Absolute URL for a server path (which may already carry a query)."""
        u = self.server_url + path
        if query:
            q = urllib.parse.urlencode([(k, _qv(v)) for k, v in query.items() if v is not None])
            if q:
                u += ("&" if "?" in u else "?") + q
        return u

    def send(self, method, path, body=None, query=None, auth=True, headers=None, timeout=None, tokens=None):
        """One attempt, no refresh."""
        if not self.server_url:
            r = Response(0)
            r.error_message = "no server"
            return r
        h = {"Accept": "application/json"}
        h.update(self.headers)
        data = None
        if body is not None or method in ("POST", "PUT", "PATCH"):
            data = json.dumps({} if body is None else body).encode("utf-8")
            h["Content-Type"] = "application/json"
        if auth:
            tok = (tokens if tokens is not None else self.store.load()).get("access_token")
            if tok:
                h["Authorization"] = "Bearer " + tok
        if headers:
            h.update(headers)
        req = urllib.request.Request(self.url(path, query), data=data, method=method, headers=h)
        try:
            with self._opener.open(req, timeout=timeout or self.timeout) as r:
                return Response(r.status, r.read(), r.headers.get("Content-Type", ""))
        except urllib.error.HTTPError as e:
            try:
                raw = e.read()
            except Exception:  # noqa: BLE001 - a broken error body is still an error
                raw = b""
            return Response(e.code, raw, e.headers.get("Content-Type", "") if e.headers else "")
        except (urllib.error.URLError, socket.timeout, ssl.SSLError, OSError, ValueError) as e:
            r = Response(0)
            r.error_message = str(getattr(e, "reason", e))
            return r

    def request(self, method, path, body=None, query=None, auth=True, headers=None, timeout=None):
        """send(), plus one refresh + replay on a 401."""
        tokens = self.store.load() if auth else {}
        resp = self.send(method, path, body, query, auth, headers, timeout, tokens)
        if resp.status == 401 and auth and tokens.get("refresh_token"):
            try:
                refreshed = self.refresh(tokens["refresh_token"])
            except TimeoutError:
                refreshed = False  # another interpreter is stuck mid-refresh
            if refreshed:
                resp = self.send(method, path, body, query, auth, headers, timeout)
        return resp

    def refresh(self, used_refresh_token):
        """Rotate the token pair after a 401. True when the caller should
        replay (new tokens stored, by this call or by another interpreter).
        A refresh the server rejects with 401 ends the sign-in; anything else
        (no answer, 5xx) keeps the tokens, so a flaky network doesn't sign
        the user out."""
        with self.store.lock():
            current = self.store.load()
            rt = current.get("refresh_token") or ""
            if not rt:
                return False
            if rt != used_refresh_token:
                return True
            resp = self.send("POST", API + "/auth/refresh", {"refresh_token": rt}, auth=False)
            pair = _token_pair(resp)
            if resp.ok and pair:
                self.store.save(merge_tokens(current, pair))
                return True
            if resp.status == 401:
                self.store.clear()
            return False

    def call(self, method, path, body=None, query=None, auth=True, headers=None, timeout=None):
        """request(), raising ApiError unless it succeeded. Returns the Response."""
        resp = self.request(method, path, body, query, auth, headers, timeout)
        if not resp.ok:
            raise ApiError(resp)
        return resp

    def get_data(self, path, query=None):
        return self.call("GET", path, query=query).data

    def fetch_artwork(self, path):
        """(status, content_type, bytes) for a /artwork/... path (with its
        query), through the access token: what the artwork proxy serves."""
        resp = self.request("GET", path, headers={"Accept": "image/*"}, timeout=30)
        if not resp.ok:
            return resp.status if resp.status > 0 else 502, "text/plain", b""
        return 200, resp.content_type or "image/jpeg", resp.raw

    # ── session ──

    def signed_in(self):
        t = self.store.load()
        return bool(t.get("access_token") and t.get("refresh_token"))

    def username(self):
        return self.store.load().get("username") or ""

    def check_server(self):
        """The public capabilities feed: answers on any OnScreen server
        without signing in. Response (ok when the URL is right)."""
        return self.send("GET", API + "/system/capabilities", auth=False, timeout=10)

    def login(self, username, password):
        """Response; on success the tokens are stored unless the account
        wants a second factor (data.totp_required + login_challenge_token)."""
        resp = self.send("POST", API + "/auth/login", {"username": username, "password": password}, auth=False)
        self._store_pair(resp)
        return resp

    def totp_verify(self, challenge_token, code):
        resp = self.send(
            "POST", API + "/auth/totp/verify", {"login_challenge_token": challenge_token, "code": code}, auth=False
        )
        self._store_pair(resp)
        return resp

    def pair_code(self):
        """POST /auth/pair/code -> data {pin, device_token, expires_at, poll_after}."""
        return self.send("POST", API + "/auth/pair/code", {}, auth=False)

    def pair_poll(self, device_token):
        """GET /auth/pair/poll: 202 while waiting, 410 when the code expired,
        200 with the token pair once someone entered the PIN (stored here)."""
        resp = self.send(
            "GET", API + "/auth/pair/poll", auth=False, headers={"Authorization": "Bearer " + device_token}
        )
        if resp.status == 200:
            self._store_pair(resp)
        return resp

    def logout(self):
        """Revoke this device's refresh token on the server, then forget the
        tokens. The server only honours the body's refresh token alongside a
        valid bearer, so rotate first when the access token has lapsed."""
        tokens = self.store.load()
        rt = tokens.get("refresh_token")
        if rt:
            resp = self.send("POST", API + "/auth/logout", {"refresh_token": rt}, tokens=tokens)
            if resp.status == 401 and self.refresh(rt):
                tokens = self.store.load()
                self.send("POST", API + "/auth/logout", {"refresh_token": tokens.get("refresh_token")}, tokens=tokens)
        self.store.clear()

    def _store_pair(self, resp):
        pair = _token_pair(resp)
        if resp.ok and pair:
            with self.store.lock():
                self.store.save(merge_tokens({}, pair))

    # ── browse ──

    def hub(self):
        return self.get_data(API + "/hub") or {}

    def libraries(self):
        return self.get_data(API + "/libraries") or []

    def library_items(self, library_id, offset=0, limit=200, sort="title", sort_dir="asc", watch=None, item_type=None):
        """One page of a library: (items, total)."""
        resp = self.call(
            "GET",
            API + "/libraries/%s/items" % _seg(library_id),
            query={
                "sort": sort,
                "sort_dir": sort_dir,
                "limit": limit,
                "offset": offset,
                "watch": watch,
                "type": item_type,
            },
        )
        return (resp.data or []), resp.total

    def item(self, item_id):
        return self.get_data(API + "/items/%s" % _seg(item_id)) or {}

    def children(self, item_id):
        return self.get_data(API + "/items/%s/children" % _seg(item_id)) or []

    def search(self, query, limit=50):
        return self.get_data(API + "/search", {"q": query, "limit": limit}) or []

    def watchlist(self, status="plan_to_watch", limit=200):
        return self.get_data(API + "/watchlist", {"status": status, "sort": "recent", "limit": limit}) or []

    def favorites(self, limit=200):
        return self.get_data(API + "/favorites", {"limit": limit}) or []

    def collection_items(self, collection_id, limit=200):
        return self.get_data(API + "/collections/%s/items" % _seg(collection_id), {"limit": limit}) or []

    def up_next(self, item_id):
        return self.get_data(API + "/items/%s/up-next" % _seg(item_id)) or {}

    def channels(self):
        return self.get_data(API + "/tv/channels") or []

    def now_next(self):
        return self.get_data(API + "/tv/channels/now-next") or []

    def recordings(self, limit=200):
        return self.get_data(API + "/tv/recordings", {"limit": limit}) or []

    def preferences(self):
        return self.get_data(API + "/users/me/preferences") or {}

    # ── watch state ──

    def set_watched(self, item_id, watched):
        self.call("POST" if watched else "DELETE", API + "/items/%s/watched" % _seg(item_id))

    def set_favorite(self, item_id, favorite):
        self.call("POST" if favorite else "DELETE", API + "/items/%s/favorite" % _seg(item_id))

    def set_watch_status(self, item_id, status):
        """status plan_to_watch / watching / completed / on_hold / dropped, or
        None to clear it."""
        path = API + "/items/%s/watch-status" % _seg(item_id)
        if status:
            self.call("PUT", path, {"status": status})
        else:
            self.call("DELETE", path)

    def dismiss_continue_watching(self, item_id):
        self.call("POST", API + "/items/%s/dismiss-continue-watching" % _seg(item_id))

    # ── playback ──

    def playback_decision(self, item_id, file_id):
        """Response (not raised: the player reads 429 / 403 refusals)."""
        return self.request("POST", API + "/items/%s/playback-decision" % _seg(item_id), {"file_id": file_id})

    def transcode_start(self, item_id, body):
        """Response; data {session_id, playlist_url, token, start_offset_sec,
        full_timeline, segment_format, ...}."""
        return self.request("POST", API + "/items/%s/transcode" % _seg(item_id), body, timeout=70)

    def transcode_stop(self, session_id, token):
        """End a session (its ffmpeg stops at once instead of at the idle
        sweep). The route wants the user's bearer; the session token in
        ?token= is revoked along with it."""
        return self.request(
            "DELETE", API + "/transcode/sessions/%s" % _seg(session_id), query={"token": token or None}, timeout=10
        )

    def claim_transfer(self, client_name):
        """POST /playback/transfer/claim: 200 {item_id, position_ms} when
        another app sent "Play on <client_name>" (claiming removes it), 204
        when nothing is waiting."""
        return self.request("POST", API + "/playback/transfer/claim", {"client_name": client_name}, timeout=10)

    def capabilities(self):
        """The server's public feature flags (live_tv, ...), or {}."""
        resp = self.send("GET", API + "/system/capabilities", auth=False, timeout=10)
        d = resp.data if resp.ok and isinstance(resp.data, dict) else {}
        return d.get("features") if isinstance(d.get("features"), dict) else {}

    def report_progress(self, item_id, body):
        return self.request("PUT", API + "/items/%s/progress" % _seg(item_id), body, timeout=10)


def merge_tokens(current, pair):
    """current with a token pair's fields over it. An asset token or username
    the response leaves out is kept from before."""
    out = dict(current)
    for k in TOKEN_FIELDS:
        v = pair.get(k)
        if v not in (None, ""):
            out[k] = v
    return out


def _token_pair(resp):
    """The token pair in a sign-in / refresh response, or None. Accepts the
    enveloped ({"data": {...}}) and the bare form."""
    d = resp.data if isinstance(resp.data, dict) else resp.body
    if not isinstance(d, dict):
        return None
    if not (isinstance(d.get("access_token"), str) and d.get("access_token")):
        return None
    if not (isinstance(d.get("refresh_token"), str) and d.get("refresh_token")):
        return None
    return d


def _seg(value):
    """One path segment, escaped."""
    return urllib.parse.quote(str(value), safe="")


def _qv(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    return v
