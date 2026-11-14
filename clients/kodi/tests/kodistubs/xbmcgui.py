"""Stand-in for xbmcgui: ListItems record what the add-on set; dialogs
answer from queues the tests fill."""

NOTIFICATION_INFO = "info"
NOTIFICATION_WARNING = "warning"
NOTIFICATION_ERROR = "error"
ALPHANUM_HIDE_INPUT = 2

# Scripted dialog answers (popped in order) and a record of what was shown.
answers = {"input": [], "select": [], "contextmenu": [], "yesno": []}
shown = []
_window_props = {}


def reset():
    for q in answers.values():
        q.clear()
    shown.clear()
    _window_props.clear()


class _Tag:
    def __init__(self):
        self.fields = {}

    def __getattr__(self, name):
        if name.startswith("set"):

            def setter(*args):
                self.fields[name[3:]] = args[0] if len(args) == 1 else args

            return setter
        raise AttributeError(name)


class ListItem:
    def __init__(self, label="", label2="", path="", offscreen=False):
        self.label = label
        self.label2 = label2
        self.path = path
        self.art = {}
        self.props = {}
        self.context = []
        self.subtitles = []
        self.mime = ""
        self.content_lookup = None
        self.video = _Tag()
        self.music = _Tag()

    def getLabel(self):  # noqa: N802
        return self.label

    def setLabel2(self, v):  # noqa: N802
        self.label2 = v

    def getVideoInfoTag(self):  # noqa: N802
        return self.video

    def getMusicInfoTag(self):  # noqa: N802
        return self.music

    def setArt(self, art):  # noqa: N802
        self.art.update(art)

    def setProperty(self, k, v):  # noqa: N802
        self.props[k] = v

    def getProperty(self, k):  # noqa: N802
        return self.props.get(k, "")

    def addContextMenuItems(self, items):  # noqa: N802
        self.context.extend(items)

    def setSubtitles(self, subs):  # noqa: N802
        self.subtitles = list(subs)

    def setMimeType(self, mime):  # noqa: N802
        self.mime = mime

    def setContentLookup(self, v):  # noqa: N802
        self.content_lookup = v

    def setPath(self, p):  # noqa: N802
        self.path = p


class Window:
    def __init__(self, wid=10000):
        self.wid = wid

    def getProperty(self, k):  # noqa: N802
        return _window_props.get(k, "")

    def setProperty(self, k, v):  # noqa: N802
        _window_props[k] = v

    def clearProperty(self, k):  # noqa: N802
        _window_props.pop(k, None)


class Dialog:
    def _next(self, kind, default):
        q = answers[kind]
        return q.pop(0) if q else default

    def input(self, heading, defaultt="", type=0, option=0, autoclose=0):  # noqa: A002
        shown.append(("input", heading))
        return self._next("input", "")

    def select(self, heading, items, *a, **k):
        shown.append(("select", heading, list(items)))
        return self._next("select", -1)

    def contextmenu(self, items):
        shown.append(("contextmenu", list(items)))
        return self._next("contextmenu", -1)

    def yesno(self, heading, message, *a, **k):
        shown.append(("yesno", message))
        return self._next("yesno", False)

    def ok(self, heading, message):
        shown.append(("ok", heading, message))
        return True

    def notification(self, heading, message, icon=None, time=5000, sound=True):
        shown.append(("notification", message))


class DialogProgress:
    def create(self, heading, message=""):
        shown.append(("progress", heading))

    def update(self, percent, message=""):
        shown.append(("progress_update", message))

    def iscanceled(self):
        return False

    def close(self):
        pass
