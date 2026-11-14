"""Build the installable add-on zip: dist/plugin.video.onscreen-<version>.zip.

    python clients/kodi/scripts/package.py

The zip holds the plugin.video.onscreen folder (Kodi's "Install from zip
file" wants the folder at the zip root), without caches or editor files.
The .po files are regenerated first, so a stale catalog can't ship.
"""

import os
import sys
import xml.etree.ElementTree as ET
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, ".."))
ADDON_DIR = os.path.join(ROOT, "plugin.video.onscreen")
DIST = os.path.join(ROOT, "dist")

SKIP_DIRS = {"__pycache__", ".pytest_cache", ".ruff_cache"}
SKIP_SUFFIXES = (".pyc", ".pyo", ".orig", ".rej", "~")


def version():
    return ET.parse(os.path.join(ADDON_DIR, "addon.xml")).getroot().get("version")


def files():
    for folder, dirs, names in os.walk(ADDON_DIR):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS and not d.startswith("."))
        for name in sorted(names):
            if name.startswith(".") or name.endswith(SKIP_SUFFIXES):
                continue
            path = os.path.join(folder, name)
            yield path, os.path.relpath(path, ROOT).replace(os.sep, "/")


def main():
    sys.path.insert(0, HERE)
    import strings

    if strings.main([]) != 0:
        return 1
    os.makedirs(DIST, exist_ok=True)
    out = os.path.join(DIST, "plugin.video.onscreen-%s.zip" % version())
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
        for path, arcname in files():
            # Fixed timestamps: the same tree always zips to the same bytes.
            info = zipfile.ZipInfo(arcname, date_time=(2020, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o644 << 16
            with open(path, "rb") as f:
                z.writestr(info, f.read())
    print(out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
