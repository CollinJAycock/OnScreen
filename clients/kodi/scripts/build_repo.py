"""Build the OnScreen Kodi repository: the static files Kodi reads.

    python clients/kodi/scripts/build_repo.py                  # -> clients/kodi/dist/repo/
    python clients/kodi/scripts/build_repo.py --out site/kodi  # into the website
    python clients/kodi/scripts/build_repo.py --base-url http://127.0.0.1:8099/  # a local test repository

The tree, as repository.onscreen's addon.xml expects it under its datadir:

    addons.xml, addons.xml.md5
    plugin.video.onscreen/plugin.video.onscreen-<version>.zip (+ .sha256)
    plugin.video.onscreen/resources/icon.png, fanart.jpg   (shown before install)
    repository.onscreen/repository.onscreen-<version>.zip (+ .sha256)
    repository.onscreen/icon.png, fanart.jpg
    index.html, repository.onscreen-<version>.zip (a copy)

Someone installs repository.onscreen-<version>.zip once ("Install from zip
file"), then the add-on from it, and Kodi updates the add-on when a newer
version appears in addons.xml. index.html lists that zip the way a web
server's directory listing does, so a TV can add https://on-screen.tv/kodi/
as a file source in Kodi and install from it without a browser. Publishing
= writing the tree into site/kodi and deploying the website.

--base-url points the repository add-on at another address, in its zip
only (the source stays on on-screen.tv), for a test repository.
"""

import argparse
import hashlib
import os
import re
import shutil
import sys
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, ".."))
ADDONS = ("plugin.video.onscreen", "repository.onscreen")
DEFAULT_BASE = "https://on-screen.tv/kodi/"

sys.path.insert(0, HERE)
import package  # noqa: E402
import strings  # noqa: E402


def addon_xml_body(addon_dir, base_url=None):
    """An add-on's addon.xml without the XML declaration (for addons.xml),
    with the repository URLs swapped for base_url when given."""
    with open(os.path.join(addon_dir, "addon.xml"), encoding="utf-8") as f:
        text = f.read()
    text = re.sub(r"^\s*<\?xml[^>]*\?>\s*", "", text)
    if base_url:
        text = text.replace(DEFAULT_BASE, base_url)
    return text.strip()


def assets(addon_dir):
    """The image files addon.xml names under <assets> (relative paths)."""
    root = ET.parse(os.path.join(addon_dir, "addon.xml")).getroot()
    out = []
    for meta in root.iter("assets"):
        for el in meta:
            if el.text and el.text.strip():
                out.append(el.text.strip())
    return out


INDEX = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OnScreen Kodi repository</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 40rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5;
  background: #0b0d12; color: #e7e9ee; }
a { color: #8ab4ff; }
code { background: #1b1f29; padding: 0 0.3em; border-radius: 4px; }
</style>
</head>
<body>
<h1>Index of /kodi/</h1>
<p>The OnScreen add-on for Kodi 20 and later. In Kodi, turn on <i>Settings &rarr; System &rarr; Add-ons &rarr;
Unknown sources</i>, add <code>https://on-screen.tv/kodi/</code> as a source in the <i>File manager</i>, use
<i>Add-ons &rarr; Install from zip file</i> on the file below, then install <i>OnScreen</i> from <i>Install from
repository &rarr; OnScreen Add-ons</i>. Kodi keeps it up to date from then on.</p>
<pre>
<a href="{zip}">{zip}</a>
</pre>
<p><a href="https://on-screen.tv/">on-screen.tv</a></p>
</body>
</html>
"""


def index_html(repo_zip_name):
    """A page that reads as a directory listing to Kodi (its HTTP source lists
    the links whose text is the file name) and as instructions to a person."""
    return INDEX.replace("{zip}", repo_zip_name)


def build(out_dir, base_url=None):
    if strings.main([]) != 0:
        raise SystemExit(1)
    if base_url and not base_url.endswith("/"):
        base_url += "/"
    if os.path.isdir(out_dir):
        shutil.rmtree(out_dir)
    os.makedirs(out_dir)
    bodies = []
    for addon_id in ADDONS:
        addon_dir = os.path.join(ROOT, addon_id)
        target = os.path.join(out_dir, addon_id)
        replace = {}
        if base_url and addon_id.startswith("repository."):
            replace[addon_id + "/addon.xml"] = (
                '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n' + addon_xml_body(addon_dir, base_url) + "\n"
            ).encode("utf-8")
        zip_path = package.build_zip(addon_dir, target, replace)
        with open(zip_path, "rb") as f:
            digest = hashlib.sha256(f.read()).hexdigest()
        with open(zip_path + ".sha256", "w", encoding="ascii", newline="\n") as f:
            f.write(digest)
        for rel in assets(addon_dir):
            dest = os.path.join(target, rel.replace("/", os.sep))
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            shutil.copyfile(os.path.join(addon_dir, rel.replace("/", os.sep)), dest)
        bodies.append(addon_xml_body(addon_dir, base_url if addon_id.startswith("repository.") else None))
    repo_dir = os.path.join(ROOT, "repository.onscreen")
    repo_zip = "repository.onscreen-%s.zip" % package.addon_version(repo_dir)
    shutil.copyfile(os.path.join(out_dir, "repository.onscreen", repo_zip), os.path.join(out_dir, repo_zip))
    with open(os.path.join(out_dir, "index.html"), "w", encoding="utf-8", newline="\n") as f:
        f.write(index_html(repo_zip))
    xml = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n<addons>\n' + "\n".join(bodies) + "\n</addons>\n"
    data = xml.encode("utf-8")
    with open(os.path.join(out_dir, "addons.xml"), "wb") as f:
        f.write(data)
    with open(os.path.join(out_dir, "addons.xml.md5"), "w", encoding="ascii", newline="\n") as f:
        f.write(hashlib.md5(data).hexdigest())  # noqa: S324 - Kodi's checksum format, not security
    return out_dir


def main(argv):
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--out", default=os.path.join(ROOT, "dist", "repo"))
    p.add_argument("--base-url", default=None)
    args = p.parse_args(argv)
    print(build(args.out, args.base_url))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
