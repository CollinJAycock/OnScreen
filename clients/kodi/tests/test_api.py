import threading
import urllib.request

import pytest

from resources.lib.onscreen import api


def client(server, tokens=None, store=None):
    return api.Client(server.url, store or api.MemoryTokenStore(tokens), headers={"X-Client-Capabilities": "x=1"})


def test_normalize_server_url():
    assert api.normalize_server_url(" 10.0.0.5:7070/ ") == "http://10.0.0.5:7070"
    assert api.normalize_server_url("https://media.example.com/onscreen/") == "https://media.example.com/onscreen"
    assert api.normalize_server_url("") == ""


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


def test_redirect_drops_authorization_across_hosts():
    h = api._RedirectHandler()
    req = urllib.request.Request("http://a.example/api", headers={"Authorization": "Bearer x"})
    same = h.redirect_request(req, None, 302, "Found", {}, "https://a.example/api")
    other = h.redirect_request(req, None, 302, "Found", {}, "https://b.example/api")
    assert same.get_header("Authorization") == "Bearer x"
    assert other.get_header("Authorization") is None
