"""The strings table, the .po files, settings.xml and addon.xml agree."""

import ast
import os
import re
import sys
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.join(HERE, "..")
ADDON = os.path.join(ROOT, "plugin.video.onscreen")
sys.path.insert(0, os.path.join(ROOT, "scripts"))

import strings  # noqa: E402

TABLE = {row[0]: row for row in strings.STRINGS}


def code_string_ids():
    """common.STRINGS, read without importing (it imports xbmc)."""
    with open(os.path.join(ADDON, "resources", "lib", "kodi", "common.py"), encoding="utf-8") as f:
        src = f.read()
    for node in ast.walk(ast.parse(src)):
        if isinstance(node, ast.Assign) and getattr(node.targets[0], "id", "") == "STRINGS":
            return ast.literal_eval(node.value)
    raise AssertionError("no STRINGS in common.py")


def test_ids_are_unique_and_in_range():
    ids = [row[0] for row in strings.STRINGS]
    assert len(ids) == len(set(ids))
    assert all(30000 <= i <= 30999 for i in ids)


def test_every_code_key_has_a_string():
    for key, sid in code_string_ids().items():
        assert sid in TABLE, "%s -> %d missing from scripts/strings.py" % (key, sid)


def test_translations_keep_placeholders():
    for sid, en, es, zh in strings.STRINGS:
        want = sorted(re.findall(r"\{(\w+)\}", en))
        assert sorted(re.findall(r"\{(\w+)\}", es)) == want, sid
        assert sorted(re.findall(r"\{(\w+)\}", zh)) == want, sid
        assert es.strip() and zh.strip(), sid


def test_po_files_are_current():
    for path, text in strings.targets():
        with open(path, encoding="utf-8", newline="") as f:
            current = f.read().replace(strings.CRLF, "\n")  # a Windows checkout has CRLF
        assert current == text, "%s is stale: run python clients/kodi/scripts/strings.py" % path


def test_settings_labels_exist():
    tree = ET.parse(os.path.join(ADDON, "resources", "settings.xml"))
    ids = set()
    for el in tree.iter():
        for attr in ("label", "help"):
            v = el.get(attr)
            if v:
                ids.add(int(v))
        if el.tag == "heading" and el.text:
            ids.add(int(el.text))
    missing = sorted(i for i in ids if i not in TABLE)
    assert not missing, missing


def test_settings_defaults_match_the_code():
    tree = ET.parse(os.path.join(ADDON, "resources", "settings.xml"))
    found = {s.get("id"): s.get("type") for s in tree.iter("setting")}
    for key in (
        "server_url",
        "max_resolution",
        "hevc",
        "av1",
        "vp9",
        "hdr_mode",
        "dovi_mode",
        "ask_version",
        "page_size",
        "art_proxy",
        "art_port",
    ):
        assert key in found, key


def test_addon_xml():
    root = ET.parse(os.path.join(ADDON, "addon.xml")).getroot()
    assert root.get("id") == "plugin.video.onscreen"
    assert re.fullmatch(r"\d+\.\d+\.\d+", root.get("version"))
    points = {e.get("point"): e for e in root.iter("extension")}
    assert points["xbmc.python.pluginsource"].get("library") == "default.py"
    assert points["xbmc.service"].get("library") == "service.py"
    for asset in ("icon", "fanart"):
        rel = points["xbmc.addon.metadata"].find("assets/" + asset).text
        assert os.path.isfile(os.path.join(ADDON, rel)), rel
