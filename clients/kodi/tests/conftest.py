"""Test setup: the add-on folder and the Kodi stand-ins on sys.path, and a
fake OnScreen server (a real HTTP server on 127.0.0.1) the client and the
plugin talk to."""

import http.server
import json
import os
import sys
import threading
import urllib.parse

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
ADDON = os.path.join(HERE, "..", "plugin.video.onscreen")
sys.path.insert(0, os.path.join(HERE, "kodistubs"))
sys.path.insert(0, ADDON)


class Request:
    def __init__(self, method, path, query, headers, body):
        self.method = method
        self.path = path
        self.query = query
        self.headers = headers
        self.body = body

    def json(self):
        return json.loads(self.body or b"{}")


class FakeOnScreen:
    """routes[(METHOD, path)] = (status, payload) or a function(Request) ->
    (status, payload[, content_type]). payload: dict / list (sent as JSON),
    bytes, or None (no body). Every request is kept in .requests."""

    def __init__(self):
        self.routes = {}
        self.requests = []
        fake = self

        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def _handle(self):
                parts = urllib.parse.urlsplit(self.path)
                n = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(n) if n else b""
                req = Request(
                    self.command, parts.path, dict(urllib.parse.parse_qsl(parts.query)), dict(self.headers), body
                )
                fake.requests.append(req)
                route = fake.routes.get((self.command, parts.path))
                if route is None:
                    status, payload, ctype = 404, {"error": {"code": "NOT_FOUND", "message": "not found"}}, None
                else:
                    out = route(req) if callable(route) else route
                    status, payload = out[0], out[1]
                    ctype = out[2] if len(out) > 2 else None
                if isinstance(payload, (dict, list)):
                    raw = json.dumps(payload).encode()
                    ctype = ctype or "application/json"
                elif payload is None:
                    raw = b""
                else:
                    raw = payload
                self.send_response(status)
                if ctype:
                    self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            do_GET = do_POST = do_PUT = do_DELETE = _handle

            def log_message(self, *a):
                pass

        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = "http://127.0.0.1:%d" % self.httpd.server_address[1]
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def calls(self, method, path):
        return [r for r in self.requests if r.method == method and r.path == path]

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


@pytest.fixture
def server():
    s = FakeOnScreen()
    yield s
    s.close()


@pytest.fixture
def kodi(tmp_path, server):
    """The Kodi stand-ins reset, a profile folder, and the add-on pointed at
    the fake server."""
    import xbmc
    import xbmcaddon
    import xbmcgui
    import xbmcplugin
    import xbmcvfs

    for m in (xbmc, xbmcgui, xbmcplugin, xbmcaddon):
        m.reset()
    xbmcaddon.PROFILE = str(tmp_path / "profile")
    xbmcvfs.TEMP_ROOT = str(tmp_path / "temp")
    xbmcaddon.settings["server_url"] = server.url
    return {"xbmc": xbmc, "gui": xbmcgui, "plugin": xbmcplugin, "addon": xbmcaddon, "tmp": tmp_path}


def sign_in(kodi_env, access="acc", refresh="ref", asset="ast", username="alice"):
    """Store a token pair as a finished sign-in would."""
    from resources.lib.kodi import common

    common.token_store().save(
        {"access_token": access, "refresh_token": refresh, "asset_token": asset, "username": username}
    )
