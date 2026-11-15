import os
import stat
import threading
import time
import urllib.request

import pytest

from resources.lib.onscreen import api


def client(server, tokens=None, store=None):
    return api.Client(server.url, store or api.MemoryTokenStore(tokens), headers={"X-Client-Capabilities": "x=1"})


def test_normalize_server_url():
    assert api.normalize_server_url(" 10.0.0.5:7070/ ") == "http://10.0.0.5:7070"
    assert api.normalize_server_url("https://media.example.com/onscreen/") == "https://media.example.com/onscreen"
    assert api.normalize_server_url("") == ""
    # Without a scheme, never plain http:// to a public host.
    assert api.normalize_server_url("media.example.com") == "https://media.example.com"
    assert api.normalize_server_url("nas:7070") == "http://nas:7070"


@pytest.mark.parametrize(
    "host",
    [
        "localhost",
        "tv.localhost",
        "nas.local",
        "box.lan",
        "media.home.arpa",
        "svc.internal",
        "nas",
        "NAS.LOCAL.",
        "10.0.0.66",
        "127.0.0.1",
        "172.16.0.1",
        "172.31.255.255",
        "192.168.1.10",
        "169.254.1.1",
        "100.64.0.1",
        "100.127.0.1",
        "::1",
        "[::1]",
        "fd12:3456::1",
        "fc00::1",
        "fe80::1",
        "febf::1",
    ],
)
def test_local_network_hosts(host):
    assert api.is_local_network_host(host)


@pytest.mark.parametrize(
    "host",
    [
        "",
        None,
        "media.example.com",
        "8.8.8.8",
        "172.15.0.1",
        "172.32.0.1",
        "192.169.1.1",
        "100.63.0.1",
        "100.128.0.1",
        "2001:db8::1",
        "fec0::1",
        "local.example.com",
    ],
)
def test_public_hosts(host):
    assert not api.is_local_network_host(host)


def test_server_url_candidates_follow_the_local_network_rule():
    # No scheme: https:// first; http:// as well only on the local network.
    assert api.server_url_candidates("media.example.com") == ["https://media.example.com"]
    assert api.server_url_candidates(" 192.168.1.10:7070/ ") == [
        "https://192.168.1.10:7070",
        "http://192.168.1.10:7070",
    ]
    assert api.server_url_candidates("nas:7070/onscreen") == ["https://nas:7070/onscreen", "http://nas:7070/onscreen"]
    # A scheme is used as typed; plain http:// only to the local network.
    assert api.server_url_candidates("HTTPS://Media.Example.com/") == ["https://media.example.com"]
    assert api.server_url_candidates("http://10.0.0.66:7070") == ["http://10.0.0.66:7070"]
    with pytest.raises(api.ServerAddressError) as e:
        api.server_url_candidates("http://media.example.com")
    assert e.value.kind == "cleartext_remote" and e.value.host == "media.example.com"
    for bad in (
        "",
        "   ",
        "ftp://nas",
        "http:/nas",
        "https:media.example.com",
        "https://",
        "http://u:p@nas",
        "https://h/?a=1",
    ):
        with pytest.raises(api.ServerAddressError) as e:
            api.server_url_candidates(bad)
        assert e.value.kind == "invalid", bad


def test_cleartext_to_a_public_host_is_never_sent():
    opened = []

    class Opener:
        def open(self, req, timeout=None):
            opened.append(req)
            raise AssertionError("sent")

    c = api.Client("http://media.example.com", api.MemoryTokenStore({"access_token": "A"}), opener=Opener())
    r = c.send("GET", api.API + "/hub")
    assert r.status == 0 and r.error_code == api.CLEARTEXT_REMOTE
    r = c.login("alice", "secret")
    assert r.status == 0 and r.error_code == api.CLEARTEXT_REMOTE
    assert opened == []
    assert api.cleartext_remote("http://media.example.com") and not api.cleartext_remote("https://media.example.com")
    assert not api.cleartext_remote("http://192.168.1.10:7070")


def test_response_unwraps_envelopes():
    r = api.Response(200, b'{"data": [1, 2], "meta": {"total": 7}}', "application/json")
    assert r.ok and r.data == [1, 2] and r.total == 7
    e = api.Response(429, b'{"error": {"code": "TOO_MANY_SESSIONS", "message": " Stop one. "}}')
    assert not e.ok and e.error_code == "TOO_MANY_SESSIONS" and e.error_message == "Stop one."
    img = api.Response(200, b"\x89PNG...", "image/png")
    assert img.body is None and img.raw.startswith(b"\x89PNG")


def test_headers_and_bearer(server):
    server.routes[("GET", "/api/v1/libraries")] = (200, {"data": [{"id": "l1"}]})
    c = client(server, {"access_token": "A", "refresh_token": "R"})
    assert c.libraries() == [{"id": "l1"}]
    req = server.requests[-1]
    assert req.headers["Authorization"] == "Bearer A"
    assert req.headers["X-Client-Capabilities"] == "x=1"


def test_401_refreshes_once_and_replays(server):
    seen = []

    def libs(req):
        seen.append(req.headers.get("Authorization"))
        if req.headers.get("Authorization") == "Bearer old":
            return 401, {"error": {"code": "UNAUTHORIZED"}}
        return 200, {"data": []}

    server.routes[("GET", "/api/v1/libraries")] = libs
    server.routes[("POST", "/api/v1/auth/refresh")] = (
        200,
        {"data": {"access_token": "new", "refresh_token": "R2", "asset_token": "S2"}},
    )
    store = api.MemoryTokenStore({"access_token": "old", "refresh_token": "R1", "asset_token": "S1", "username": "u"})
    c = client(server, store=store)
    assert c.libraries() == []
    assert seen == ["Bearer old", "Bearer new"]
    assert server.calls("POST", "/api/v1/auth/refresh")[0].json() == {"refresh_token": "R1"}
    t = store.load()
    assert (t["access_token"], t["refresh_token"], t["asset_token"], t["username"]) == ("new", "R2", "S2", "u")


def test_refresh_skipped_when_another_process_already_rotated(server):
    """The token on disk changed while this request was in flight: replay
    with it, never spend the old refresh token (the server would treat a
    reused refresh token as theft and end the sign-in)."""
    store = api.MemoryTokenStore({"access_token": "old", "refresh_token": "R1"})
    c = client(server, store=store)
    store.save({"access_token": "fresh", "refresh_token": "R2"})
    assert c.refresh("R1") is True
    assert server.calls("POST", "/api/v1/auth/refresh") == []


def test_refresh_rejected_signs_out(server):
    server.routes[("POST", "/api/v1/auth/refresh")] = (401, {"error": {"code": "UNAUTHORIZED"}})
    server.routes[("GET", "/api/v1/hub")] = (401, {"error": {"code": "UNAUTHORIZED"}})
    store = api.MemoryTokenStore({"access_token": "a", "refresh_token": "r"})
    c = client(server, store=store)
    with pytest.raises(api.ApiError) as e:
        c.hub()
    assert e.value.signed_out
    assert store.load() == {}


def test_refresh_server_error_keeps_tokens(server):
    server.routes[("POST", "/api/v1/auth/refresh")] = (503, {"error": {"code": "UNAVAILABLE"}})
    server.routes[("GET", "/api/v1/hub")] = (401, {})
    store = api.MemoryTokenStore({"access_token": "a", "refresh_token": "r"})
    with pytest.raises(api.ApiError):
        client(server, store=store).hub()
    assert store.load()["refresh_token"] == "r"


def test_network_error_is_status_zero():
    c = api.Client("http://127.0.0.1:9", api.MemoryTokenStore())
    r = c.send("GET", "/api/v1/system/capabilities", auth=False, timeout=2)
    assert r.status == 0 and r.network_error


def test_login_stores_pair_and_totp_challenge(server):
    server.routes[("POST", "/api/v1/auth/login")] = (
        200,
        {"data": {"totp_required": True, "login_challenge_token": "ch"}},
    )
    store = api.MemoryTokenStore()
    c = client(server, store=store)
    r = c.login("alice", "pw")
    assert r.data["totp_required"] and not c.signed_in()
    server.routes[("POST", "/api/v1/auth/totp/verify")] = (
        200,
        {"data": {"access_token": "A", "refresh_token": "R", "username": "alice"}},
    )
    c.totp_verify("ch", "123456")
    assert c.signed_in() and c.username() == "alice"
    assert server.calls("POST", "/api/v1/auth/totp/verify")[0].json() == {
        "login_challenge_token": "ch",
        "code": "123456",
    }


def test_pair_poll_uses_device_token(server):
    state = {"n": 0}

    def poll(req):
        assert req.headers["Authorization"] == "Bearer dev-tok"
        state["n"] += 1
        if state["n"] == 1:
            return 202, {"data": {"status": "pending"}}
        return 200, {"data": {"access_token": "A", "refresh_token": "R", "asset_token": "S", "username": "bob"}}

    server.routes[("GET", "/api/v1/auth/pair/poll")] = poll
    store = api.MemoryTokenStore()
    c = client(server, store=store)
    assert c.pair_poll("dev-tok").status == 202 and not c.signed_in()
    assert c.pair_poll("dev-tok").status == 200 and c.username() == "bob"


def test_logout_revokes_then_forgets(server):
    server.routes[("POST", "/api/v1/auth/logout")] = (204, None)
    store = api.MemoryTokenStore({"access_token": "A", "refresh_token": "R"})
    client(server, store=store).logout()
    assert server.calls("POST", "/api/v1/auth/logout")[0].json() == {"refresh_token": "R"}
    assert store.load() == {}


def test_library_items_query_and_total(server):
    server.routes[("GET", "/api/v1/libraries/lib%201/items")] = (200, {"data": [{"id": "m"}], "meta": {"total": 451}})
    items, total = client(server, {"access_token": "A"}).library_items(
        "lib 1", offset=200, limit=200, watch="unwatched"
    )
    assert items == [{"id": "m"}] and total == 451
    q = server.requests[-1].query
    assert q == {"sort": "title", "sort_dir": "asc", "limit": "200", "offset": "200", "watch": "unwatched"}


def test_fetch_artwork_returns_bytes(server):
    server.routes[("GET", "/artwork/Movies/A/poster.jpg")] = (200, b"JPEGDATA", "image/jpeg")
    status, ctype, body = client(server, {"access_token": "A"}).fetch_artwork("/artwork/Movies/A/poster.jpg?w=500")
    assert (status, ctype, body) == (200, "image/jpeg", b"JPEGDATA")
    assert server.requests[-1].query == {"w": "500"}


def test_file_store_roundtrip_and_lock(tmp_path):
    store = api.FileTokenStore(str(tmp_path / "p" / "auth.json"), lock_timeout=0.3)
    assert store.load() == {}
    store.save({"access_token": "A", "refresh_token": "R", "junk": "x"})
    assert store.load() == {"access_token": "A", "refresh_token": "R"}
    with store.lock():
        with pytest.raises(TimeoutError):
            with api.FileTokenStore(store.path, lock_timeout=0.2).lock():
                pass
    with store.lock():  # released
        pass
    store.clear()
    assert store.load() == {}


def test_file_lock_takes_over_a_stale_lock(tmp_path):
    store = api.FileTokenStore(str(tmp_path / "auth.json"), lock_timeout=1, stale=0)
    open(store.lock_path, "w").close()
    with store.lock():
        pass


@pytest.mark.skipif(os.name == "nt", reason="POSIX permission bits")
def test_token_file_is_owner_only(tmp_path):
    store = api.FileTokenStore(str(tmp_path / "auth.json"))
    old = os.umask(0o022)
    try:
        open(store.path, "w").close()  # an older build's file, 0644
        os.chmod(store.path, 0o644)
        store.save({"access_token": "A", "refresh_token": "R"})
    finally:
        os.umask(old)
    assert stat.S_IMODE(os.stat(store.path).st_mode) == 0o600
    assert store.load() == {"access_token": "A", "refresh_token": "R"}


def test_file_lock_ignores_the_wall_clock(tmp_path):
    # A live lock whose mtime is years old, as after an NTP jump on a box
    # without an RTC: not taken over (the wall-clock age used to say stale).
    store = api.FileTokenStore(str(tmp_path / "auth.json"), lock_timeout=0.3, stale=5)
    with open(store.lock_path, "w") as f:
        f.write("123")
    os.utime(store.lock_path, (0, 0))
    with pytest.raises(TimeoutError):
        with store.lock():
            pass
    assert os.path.exists(store.lock_path)


def test_file_lock_times_out_when_it_can_never_create_the_file(tmp_path, monkeypatch):
    # PermissionError on create with no lock file there (an unwritable
    # folder) used to loop without sleeping or checking the deadline.
    real_open = os.open

    def refuse(path, *a, **k):
        if str(path).endswith(".lock"):
            raise PermissionError(path)
        return real_open(path, *a, **k)

    monkeypatch.setattr(api.os, "open", refuse)
    store = api.FileTokenStore(str(tmp_path / "auth.json"), lock_timeout=0.2)
    errors = []

    def take():
        try:
            with store.lock():
                pass
        except TimeoutError as e:
            errors.append(e)

    t = threading.Thread(target=take, daemon=True)
    t.start()
    t.join(5)
    assert not t.is_alive() and len(errors) == 1


def test_file_lock_dead_holder_taken_over_by_one_waiter(tmp_path):
    store = api.FileTokenStore(str(tmp_path / "auth.json"), lock_timeout=10, stale=0.3)
    with open(store.lock_path, "w") as f:
        f.write("99999")  # a holder that died
    inside = []
    overlap = []
    errors = []

    def work():
        try:
            for _ in range(30):
                with store.lock():
                    if inside:
                        overlap.append(1)
                    inside.append(1)
                    time.sleep(0.001)
                    inside.pop()
        except Exception as e:  # noqa: BLE001 - surfaced by the assert below
            errors.append(e)

    threads = [threading.Thread(target=work) for _ in range(6)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert errors == [] and overlap == []


def test_file_lock_serializes_threads(tmp_path):
    store = api.FileTokenStore(str(tmp_path / "auth.json"))
    inside = []
    overlap = []
    errors = []

    def work():
        try:
            for _ in range(200):
                with store.lock():
                    if inside:
                        overlap.append(1)
                    inside.append(1)
                    inside.pop()
        except Exception as e:  # noqa: BLE001 - surfaced by the assert below
            errors.append(e)

    threads = [threading.Thread(target=work) for _ in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert errors == []  # Windows: PermissionError while a lock file is being deleted
    assert overlap == []


def test_redirect_keeps_authorization_only_on_the_same_origin():
    h = api._RedirectHandler()
    req = urllib.request.Request("https://a.example/api", headers={"Authorization": "Bearer x"})

    def auth_after(newurl, src=req):
        return h.redirect_request(src, None, 302, "Found", {}, newurl).get_header("Authorization")

    assert auth_after("https://a.example/api/v1/") == "Bearer x"
    assert auth_after("https://A.example:443/other") == "Bearer x"  # the default port, spelled out
    assert auth_after("https://b.example/api") is None  # another host
    assert auth_after("http://a.example/api") is None  # an https -> http downgrade
    assert auth_after("https://a.example:8443/api") is None  # another port
    plain = urllib.request.Request("http://10.0.0.5:7070/api", headers={"Authorization": "Bearer x"})
    assert auth_after("https://10.0.0.5/api", plain) is None  # http -> https changes scheme and port
