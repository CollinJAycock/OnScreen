"""Build the installable add-on zip: dist/plugin.video.onscreen-<version>.zip.

    python clients/kodi/scripts/package.py

The zip holds the plugin.video.onscreen folder (Kodi's "Install from zip
file" wants the folder at the zip root), without caches or editor files.
The .po files are regenerated first, so a stale catalog can't ship.
scripts/build_repo.py uses build_zip for the repository too.
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


def addon_version(addon_dir):
    return ET.parse(os.path.join(addon_dir, "addon.xml")).getroot().get("version")


def version():
    return addon_version(ADDON_DIR)


def files(addon_dir):
    """(path on disk, path in the zip) for every file the add-on ships."""
    parent = os.path.dirname(addon_dir)
    for folder, dirs, names in os.walk(addon_dir):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS and not d.startswith("."))
        for name in sorted(names):
            if name.startswith(".") or name.endswith(SKIP_SUFFIXES):
                continue
            path = os.path.join(folder, name)
            yield path, os.path.relpath(path, parent).replace(os.sep, "/")


def build_zip(addon_dir, out_dir, replace=None):
    """Zip addon_dir as <id>-<version>.zip in out_dir; returns its path.
    replace maps a path in the zip to bytes that stand in for the file on
    disk (build_repo.py points a test repository at another URL this way).
    Fixed timestamps: the same tree always zips to the same bytes."""
    addon_id = os.path.basename(os.path.normpath(addon_dir))
    os.makedirs(out_dir, exist_ok=True)
    out = os.path.join(out_dir, "%s-%s.zip" % (addon_id, addon_version(addon_dir)))
    replace = replace or {}
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
        for path, arcname in files(addon_dir):
            info = zipfile.ZipInfo(arcname, date_time=(2020, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o644 << 16
            if arcname in replace:
                data = replace[arcname]
            else:
                with open(path, "rb") as f:
                    data = f.read()
            z.writestr(info, data)
    return out


def main():
    sys.path.insert(0, HERE)
    import strings

    if strings.main([]) != 0:
        return 1
    print(build_zip(ADDON_DIR, DIST))
    return 0


if __name__ == "__main__":
    sys.exit(main())
