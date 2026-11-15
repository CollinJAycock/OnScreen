"""A local HTTP proxy for the server's artwork, run by the add-on's service.

Why: /artwork/* needs a credential, and the only one that fits in a URL is the
asset token, which lasts 24 h. Kodi caches images by URL, so a token in the
URL would make it download every poster again each day and keep the old
copies. Through the proxy the URL Kodi sees is stable,
http://127.0.0.1:<port>/artwork/<path>?w=500, and the proxy adds the
current access token as a header on its way to the server.

Photos go the same way: /photo/<item id>.jpg?w=1920 is the server's
/api/v1/items/<id>/image (resized to JPEG; the .jpg tells Kodi's picture
viewer what it is).

Only /artwork/ and /photo/ are served, only to loopback, and only with a
loopback Host header (a web page can't reach it by rebinding a DNS name to
127.0.0.1).

A HEAD is answered here, without fetching the image (the server's /artwork
route has no HEAD, so it used to cost a full download thrown away). An idle
keep-alive connection is closed after IDLE_TIMEOUT, and at most
MAX_CONNECTIONS are served at once, which also bounds the images held in
memory.
"""

import http.server
import re
import socket
import threading
import urllib.parse

ALLOWED_PARAMS = ("w", "h")
LOOPBACK_HOSTS = ("127.0.0.1", "localhost", "[::1]")
_PHOTO = re.compile(r"^/photo/([0-9A-Fa-f-]{8,64})\.jpg$")
IDLE_TIMEOUT = 30  # seconds a connection may sit between requests
MAX_CONNECTIONS = 16
SLOT_WAIT = 5  # seconds a new connection waits for a free slot before it's dropped
_TYPES = {".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".gif": "image/gif"}


def proxy_url(port, path, width=0):
    """The proxy URL for a server artwork path (as the API gives it:
    "Movies/Film (2020)/poster.jpg", no leading slash). width 0 asks for the
    original (clear logos are transparent PNGs; resizing makes them JPEG)."""
    url = "http://127.0.0.1:%d/artwork/%s" % (port, urllib.parse.quote(path.lstrip("/"), safe="/"))
    if width:
        url += "?w=%d" % width
    return url


def photo_url(port, item_id, width=0):
    """The proxy URL for a photo item's image."""
    url = "http://127.0.0.1:%d/photo/%s.jpg" % (port, urllib.parse.quote(item_id, safe=""))
    if width:
        url += "?w=%d" % width
    return url


def server_path(request_path):
    """The server path and query to fetch for a proxy request path, or None
    when the request isn't for artwork or a photo. Keeps only the resize
    parameters."""
    parts = urllib.parse.urlsplit(request_path)
    query = [(k, v) for k, v in urllib.parse.parse_qsl(parts.query) if k in ALLOWED_PARAMS and v.isdigit()]
    photo = _PHOTO.match(parts.path)
    if photo:
        out = "/api/v1/items/%s/image" % photo.group(1)
        return out + ("?" + urllib.parse.urlencode(query) if query else "")
    if not parts.path.startswith("/artwork/") or len(parts.path) <= len("/artwork/"):
        return None
    for s in parts.path[len("/artwork/") :].split("/"):
        u = urllib.parse.unquote(s)
        # An encoded slash or backslash inside a segment ("..%2fapi%2fv1")
        # becomes a path step out of /artwork/ behind a reverse proxy that
        # decodes %2F, with the user's bearer attached.
        if s in ("", ".", "..") or u in (".", "..") or "/" in u or "\\" in u:
            return None
    out = parts.path
    if query:
        out += "?" + urllib.parse.urlencode(query)
    return out


def guessed_type(path):
    """The Content-Type a GET of server path is likely to have, for a HEAD
    answered without fetching: a resized image (and a photo) is a JPEG, an
    original goes by its extension."""
    parts = urllib.parse.urlsplit(path)
    if parts.path.startswith("/api/v1/items/") or parts.query:
        return "image/jpeg"
    dot = parts.path.rfind(".")
    return _TYPES.get(parts.path[dot:].lower() if dot >= 0 else "", "application/octet-stream")


def host_allowed(host_header, port):
    host = (host_header or "").strip().lower()
    for h in LOOPBACK_HOSTS:
        if host in (h, "%s:%d" % (h, port)):
            return True
    return False


class _Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "OnScreenArt/1"
    # Kodi keeps connections alive; without a timeout every idle one pinned
    # a thread for good.
    timeout = IDLE_TIMEOUT

    def do_GET(self):  # noqa: N802 - http.server's name
        self._serve(True)

    def do_HEAD(self):  # noqa: N802
        self._serve(False)

    def _serve(self, with_body):
        proxy = self.server.proxy
        if not host_allowed(self.headers.get("Host"), self.server.server_address[1]):
            self._reply(403, b"", "text/plain", with_body)
            return
        path = server_path(self.path)
        if path is None:
            self._reply(404, b"", "text/plain", with_body)
            return
        if not with_body:
            # A HEAD: no length, as that isn't known without the download.
            self.send_response(200)
            self.send_header("Content-Type", guessed_type(path))
            self.end_headers()
            return
        try:
            status, content_type, body = proxy.fetch(path)
        except Exception as e:  # noqa: BLE001 - one bad image must not kill the server thread
            proxy.log("artwork %s: %s" % (path, e))
            status, content_type, body = 502, "text/plain", b""
        self._reply(status, body, content_type, with_body)

    def _reply(self, status, body, content_type, with_body):
        self.send_response(status)
        self.send_header("Content-Type", content_type or "application/octet-stream")
        self.send_header("Content-Length", str(len(body)))
        if status == 200:
            self.send_header("Cache-Control", "max-age=31536000")
        self.end_headers()
        if with_body and body:
            try:
                self.wfile.write(body)
            except (ConnectionError, socket.timeout):
                pass

    def log_message(self, fmt, *args):  # quiet: Kodi's log gets errors only
        pass


class _Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = False

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._slots = threading.BoundedSemaphore(MAX_CONNECTIONS)

    def process_request(self, request, client_address):
        # One thread per connection, at most MAX_CONNECTIONS of them. One past
        # that waits a moment for a slot, then is dropped (Kodi asks again),
        # so the accept loop never holds up a shutdown for long.
        if not self._slots.acquire(timeout=SLOT_WAIT):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except Exception:
            self._slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self._slots.release()


class ArtProxy:
    """fetch(path) -> (status, content_type, body) gets one image from the
    server (the service wires it to the API client, which refreshes the
    access token on a 401). log(message) reports errors."""

    def __init__(self, fetch, log=None):
        self.fetch = fetch
        self.log = log or (lambda _m: None)
        self._server = None
        self._thread = None

    @property
    def port(self):
        return self._server.server_address[1] if self._server else 0

    def start(self, port, tries=10):
        """Listen on 127.0.0.1:port, or the next free port of `tries`.
        Returns the port, 0 when none was free."""
        for p in range(port, port + tries):
            try:
                srv = _Server(("127.0.0.1", p), _Handler)
            except OSError:
                continue
            srv.proxy = self
            self._server = srv
            self._thread = threading.Thread(target=srv.serve_forever, name="onscreen-art", daemon=True)
            self._thread.start()
            return p
        return 0

    def stop(self):
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
            self._server = None
            self._thread = None
