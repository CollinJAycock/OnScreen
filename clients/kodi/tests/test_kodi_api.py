"""Every Kodi API name the add-on uses exists in a real Kodi.

tests/kodistubs stand in for Kodi under pytest, so a misspelt or invented
name (xbmcplugin.SORT_METHOD_YEAR, which Kodi doesn't have) passes the other
tests and fails only inside Kodi. kodi21_api.json is dir() of the real
modules and classes, dumped from Kodi 21.3; this test reads the add-on's
source and checks each xbmc*.NAME it touches, and each InfoTag setter it
calls by name, against it.
"""

import ast
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
ADDON = os.path.join(HERE, "..", "plugin.video.onscreen")
with open(os.path.join(HERE, "kodi21_api.json"), encoding="utf-8") as _f:
    API = json.load(_f)
MODULES = ("xbmc", "xbmcgui", "xbmcplugin", "xbmcaddon", "xbmcvfs")
# camelCase names from the standard library, not Kodi.
STDLIB = {"RLock", "BoundedSemaphore"}


def sources():
    for folder, _dirs, names in os.walk(ADDON):
        for name in names:
            if name.endswith(".py"):
                path = os.path.join(folder, name)
                with open(path, encoding="utf-8") as f:
                    yield path, ast.parse(f.read(), path)


def test_module_attributes_exist():
    missing = []
    for path, tree in sources():
        for node in ast.walk(tree):
            if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name) and node.value.id in MODULES:
                if node.attr not in API[node.value.id]:
                    missing.append("%s: %s.%s" % (os.path.relpath(path, ADDON), node.value.id, node.attr))
    assert not missing, missing


def test_info_tag_setters_exist():
    """listitems._call(tag, "setX", ...) names a setter as a string."""
    missing = []
    known = set(API["InfoTagVideo"]) | set(API["InfoTagMusic"])
    for path, tree in sources():
        for node in ast.walk(tree):
            if (
                isinstance(node, ast.Call)
                and getattr(node.func, "id", getattr(node.func, "attr", "")) == "_call"
                and len(node.args) >= 2
                and isinstance(node.args[1], ast.Constant)
                and str(node.args[1].value).startswith("set")
                and node.args[1].value not in known
            ):
                missing.append("%s: %s" % (os.path.relpath(path, ADDON), node.args[1].value))
    assert not missing, missing


def test_kodi_object_methods_exist():
    """Methods called on ListItems, dialogs, the player, the monitor and the
    add-on object: each name must exist on one of those classes (a name the
    add-on defines itself is fine too)."""
    kodi_methods = set()
    for cls in ("ListItem", "Dialog", "DialogProgress", "Window", "Player", "Monitor", "Addon"):
        kodi_methods |= set(API[cls])
    own = set()
    for _path, tree in sources():
        for node in ast.walk(tree):
            if isinstance(node, (ast.FunctionDef, ast.ClassDef)):
                own.add(node.name)
    camel = []
    for path, tree in sources():
        for node in ast.walk(tree):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                if isinstance(node.func.value, ast.Name) and node.func.value.id in MODULES:
                    continue  # xbmc*.function(): test_module_attributes_exist
                name = node.func.attr
                # Kodi's methods are camelCase; the add-on's and the standard
                # library's are snake_case or one word.
                if any(c.isupper() for c in name[1:]) and name not in own | kodi_methods | STDLIB:
                    camel.append("%s: %s" % (os.path.relpath(path, ADDON), name))
    assert not camel, camel
