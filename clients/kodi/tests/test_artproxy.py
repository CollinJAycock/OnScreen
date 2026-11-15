import http.client
import socket

import pytest

from resources.lib.onscreen import artproxy


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def test_proxy_url():
    assert artproxy.proxy_url(5000, "Movies/A Film (2020)/poster.jpg", 500) == (
        "http://127.0.0.1:5000/artwork/Movies/A%20Film%20%282020%29/poster.jpg?w=500"
    )
    assert artproxy.proxy_url(5000, "/x/logo.png") == "http://127.0.0.1:5000/artwork/x/logo.png"


def test_server_path_keeps_only_artwork_and_resize():
    assert artproxy.server_path("/artwork/a/b.jpg?w=500&token=zzz&h=x") == "/artwork/a/b.jpg?w=500"
    assert artproxy.server_path("/artwork/a/b.jpg") == "/artwork/a/b.jpg"
    assert artproxy.server_path("/api/v1/hub") is None
    assert artproxy.server_path("/artwork/") is None
    assert artproxy.server_path("/artwork/../etc/passwd") is None
    assert artproxy.server_path("/artwork/a/%2e%2e/b.jpg") is None
    # An encoded slash or backslash inside a segment: a path step once a
    # reverse proxy decodes it.
    assert artproxy.server_path("/artwork/..%2fapi%2fv1%2fusers%2fme") is None
    assert artproxy.server_path("/artwork/a/%2Fetc/passwd") is None
    assert artproxy.server_path("/artwork/a%5c..%5cb.jpg") is None
    # A name with a percent sign of its own is still artwork.
    assert artproxy.server_path("/artwork/Movies/100%25%20Love/poster.jpg") == (
        "/artwork/Movies/100%25%20Love/poster.jpg"
    )


def test_host_allowed():
    assert artproxy.host_allowed("127.0.0.1:5000", 5000)
    assert artproxy.host_allowed("localhost", 5000)
    assert not artproxy.host_allowed("evil.example:5000", 5000)
    assert not artproxy.host_allowed("", 5000)


@pytest.fixture
def proxy():
    fetched = []

    def fetch(path):
        fetched.append(path)
        if "missing" in path:
            return 404, "text/plain", b""
        return 200, "image/png", b"PNG!"

    p = artproxy.ArtProxy(fetch)
    port = p.start(free_port())
    assert port
    yield p, fetched
    p.stop()


def get(port, path, host=None):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    conn.request("GET", path, headers={"Host": host or "127.0.0.1:%d" % port})
    r = conn.getresponse()
    body = r.read()
    conn.close()
    return r.status, r.getheader("Content-Type"), r.getheader("Cache-Control"), body


def test_serves_artwork(proxy):
    p, fetched = proxy
    status, ctype, cache, body = get(p.port, "/artwork/Movies/A/poster.jpg?w=500")
    assert (status, ctype, body) == (200, "image/png", b"PNG!")
    assert "max-age" in cache
    assert fetched == ["/artwork/Movies/A/poster.jpg?w=500"]


def test_refuses_other_paths_and_hosts(proxy):
    p, fetched = proxy
    assert get(p.port, "/api/v1/hub")[0] == 404
    assert get(p.port, "/artwork/a.jpg", host="attacker.example")[0] == 403
    assert get(p.port, "/artwork/missing.jpg")[0] == 404
    assert fetched == ["/artwork/missing.jpg"]


def test_head_is_answered_without_a_download(proxy):
    p, fetched = proxy
    conn = http.client.HTTPConnection("127.0.0.1", p.port, timeout=5)
    for path, ctype in (
        ("/artwork/Movies/A/poster.jpg?w=500", "image/jpeg"),
        ("/artwork/x/logo.png", "image/png"),
        ("/photo/0ecfaca8-cdd7-44cb-a51e-9f75a90d4330.jpg?w=480", "image/jpeg"),
    ):
        conn.request("HEAD", path, headers={"Host": "127.0.0.1:%d" % p.port})
        r = conn.getresponse()
        r.read()
        assert (r.status, r.getheader("Content-Type")) == (200, ctype)
    conn.request("HEAD", "/api/v1/hub", headers={"Host": "127.0.0.1:%d" % p.port})
    r = conn.getresponse()
    r.read()
    assert r.status == 404
    conn.close()
    assert fetched == []  # nothing downloaded for a HEAD


def test_idle_connections_are_closed(proxy, monkeypatch):
    monkeypatch.setattr(artproxy._Handler, "timeout", 0.3)
    p, _ = proxy
    s = socket.create_connection(("127.0.0.1", p.port), timeout=5)
    try:
        s.sendall(b"GET /artwork/a.jpg HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n" % p.port)
        reply = b""
        while b"PNG!" not in reply:
            reply += s.recv(4096)
        # Kept alive, then idle: the proxy hangs up instead of holding a
        # thread on it for good.
        assert s.recv(4096) == b""
    finally:
        s.close()


def test_connections_are_capped(monkeypatch):
    monkeypatch.setattr(artproxy, "MAX_CONNECTIONS", 2)
    monkeypatch.setattr(artproxy, "SLOT_WAIT", 0.2)
    p = artproxy.ArtProxy(lambda path: (200, "image/png", b"PNG!"))
    port = p.start(free_port())
    held = [socket.create_connection(("127.0.0.1", port), timeout=5) for _ in range(2)]
    try:
        for s in held:  # each holds a slot (an open, idle keep-alive)
            s.sendall(b"GET /artwork/a.jpg HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n" % port)
            assert b"200 OK" in s.recv(4096)
        third = socket.create_connection(("127.0.0.1", port), timeout=5)
        third.sendall(b"GET /artwork/a.jpg HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n" % port)
        assert third.recv(4096) == b""  # dropped: no slot came free
        third.close()
        held.pop().close()  # frees a slot
        status, _, _, body = get(port, "/artwork/a.jpg")
        assert (status, body) == (200, b"PNG!")
    finally:
        for s in held:
            s.close()
        p.stop()


def test_next_port_when_taken():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    taken = s.getsockname()[1]
    s.listen(1)
    try:
        p = artproxy.ArtProxy(lambda path: (200, "image/png", b""))
        port = p.start(taken, tries=5)
        assert port and port != taken
        p.stop()
    finally:
        s.close()


def test_photo_route():
    assert artproxy.photo_url(5000, "0ecfaca8-cdd7-44cb-a51e-9f75a90d4330", 480) == (
        "http://127.0.0.1:5000/photo/0ecfaca8-cdd7-44cb-a51e-9f75a90d4330.jpg?w=480"
    )
    assert artproxy.server_path("/photo/0ecfaca8-cdd7-44cb-a51e-9f75a90d4330.jpg?w=480&token=x") == (
        "/api/v1/items/0ecfaca8-cdd7-44cb-a51e-9f75a90d4330/image?w=480"
    )
    assert artproxy.server_path("/photo/../../etc.jpg") is None
    assert artproxy.server_path("/photo/abc.png") is None
