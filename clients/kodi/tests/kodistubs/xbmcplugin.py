"""Stand-in for xbmcplugin: records the directory and the resolved item."""

SORT_METHOD_UNSORTED = 0
SORT_METHOD_LABEL = 1
SORT_METHOD_LABEL_IGNORE_THE = 2
SORT_METHOD_VIDEO_YEAR = 3
SORT_METHOD_VIDEO_RATING = 4
SORT_METHOD_DURATION = 5
SORT_METHOD_EPISODE = 6
SORT_METHOD_TRACKNUM = 7

directory = []
ended = []
content = []
sorts = []
resolved = []


def reset():
    for x in (directory, ended, content, sorts, resolved):
        x.clear()


def addDirectoryItems(handle, items, total=0):  # noqa: N802
    directory.extend(items)
    return True


def addDirectoryItem(handle, url, listitem, isFolder=False, totalItems=0):  # noqa: N802, N803
    directory.append((url, listitem, isFolder))
    return True


def endOfDirectory(handle, succeeded=True, updateListing=False, cacheToDisc=True):  # noqa: N802, N803
    ended.append(succeeded)


def setContent(handle, value):  # noqa: N802
    content.append(value)


def addSortMethod(handle, method, labelMask="", label2Mask=""):  # noqa: N802, N803
    sorts.append(method)


def setResolvedUrl(handle, succeeded, listitem):  # noqa: N802
    resolved.append((succeeded, listitem))
