import hashlib
import os
import sys
import xml.etree.ElementTree as ET
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "scripts"))

import build_repo  # noqa: E402


def test_repository_tree(tmp_path):
    out = str(tmp_path / "repo")
    build_repo.build(out, base_url="http://127.0.0.1:8099")
    with open(os.path.join(out, "addons.xml"), "rb") as f:
        data = f.read()
    with open(os.path.join(out, "addons.xml.md5")) as f:
        assert f.read() == hashlib.md5(data).hexdigest()
    root = ET.fromstring(data)
    versions = {a.get("id"): a.get("version") for a in root.findall("addon")}
    assert set(versions) == {"plugin.video.onscreen", "repository.onscreen"}
    # The listing points the repository at the test address...
    info = root.find("addon[@id='repository.onscreen']/extension/dir/info").text
    assert info == "http://127.0.0.1:8099/addons.xml"
    for addon_id, version in versions.items():
        z = os.path.join(out, addon_id, "%s-%s.zip" % (addon_id, version))
        with open(z, "rb") as f:
            digest = hashlib.sha256(f.read()).hexdigest()
        with open(z + ".sha256") as f:
            assert f.read() == digest
        with zipfile.ZipFile(z) as zf:
            names = zf.namelist()
            assert all(n.startswith(addon_id + "/") for n in names)
            assert addon_id + "/addon.xml" in names
            assert not any("__pycache__" in n or n.endswith(".pyc") for n in names)
            if addon_id == "repository.onscreen":
                # ...and so does the repository add-on inside its zip.
                inner = zf.read(addon_id + "/addon.xml").decode("utf-8")
                assert "http://127.0.0.1:8099/addons.xml.md5" in inner and "on-screen.tv/kodi" not in inner
                ET.fromstring(inner.split("?>", 1)[1])
    for rel in ("plugin.video.onscreen/resources/icon.png", "repository.onscreen/icon.png"):
        assert os.path.isfile(os.path.join(out, rel))


def test_default_repository_url():
    with open(os.path.join(build_repo.ROOT, "repository.onscreen", "addon.xml"), encoding="utf-8") as f:
        text = f.read()
    assert build_repo.DEFAULT_BASE + "addons.xml" in text
