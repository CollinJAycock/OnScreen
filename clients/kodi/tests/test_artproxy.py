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
