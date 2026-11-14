"""Stand-in for xbmcvfs: special:// paths map under TEMP_ROOT."""

import os

TEMP_ROOT = ""


def translatePath(path):  # noqa: N802
    if path.startswith("special://temp/"):
        return os.path.join(TEMP_ROOT, path[len("special://temp/") :])
    return path
