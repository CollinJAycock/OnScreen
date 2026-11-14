"""A local HTTP proxy for the server's artwork, run by the add-on's service.

Why: /artwork/* needs a credential, and the only one that fits in a URL is the
asset token, which lasts 24 h. Kodi caches images by URL, so a token in the
URL would make it download every poster again each day and keep the old
copies. Through the proxy the URL Kodi sees is stable,
http://127.0.0.1:<port>/artwork/<path>?w=500, and the proxy adds the
current access token as a header on its way to the server.

Only /artwork/ is served, only to loopback, and only with a loopback Host
header (a web page can't reach it by rebinding a DNS name to 127.0.0.1).
"""

import http.server
import socket
import threading
import urllib.parse

ALLOWED_PARAMS = ("w", "h")
LOOPBACK_HOSTS = ("127.0.0.1", "localhost", "[::1]")


def proxy_url(port, path, width=0):
    """The proxy URL for a server artwork path (as the API gives it:
    "Movies/Film (2020)/poster.jpg", no leading slash). width 0 asks for the
    original (clear logos are transparent PNGs; resizing makes them JPEG)."""
    url = "http://127.0.0.1:%d/artwork/%s" % (port, urllib.parse.quote(path.lstrip("/"), safe="/"))
    if width:
        url += "?w=%d" % width
    return url


def server_path(request_path):
    """The server path and query to fetch for a proxy request path, or None
    when the request isn't for artwork. Keeps only the resize parameters."""
    parts = urllib.parse.urlsplit(request_path)
    if not parts.path.startswith("/artwork/") or len(parts.path) <= len("/artwork/"):
        return None
    segments = parts.path[len("/artwork/") :].split("/")
    if any(s in ("", ".", "..") for s in segments) or any(urllib.parse.unquote(s) in (".", "..") for s in segments):
        return None
    query = [(k, v) for k, v in urllib.parse.parse_qsl(parts.query) if k in ALLOWED_PARAMS and v.isdigit()]
    out = parts.path
    if query:
        out += "?" + urllib.parse.urlencode(query)
    return out


def host_allowed(host_header, port):
    host = (host_header or "").strip().lower()
    for h in LOOPBACK_HOSTS:
        if host in (h, "%s:%d" % (h, port)):
            return True
    return False


class _Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "OnScreenArt/1"

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
