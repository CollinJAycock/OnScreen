"""The parts of the OnScreen Kodi add-on that don't import Kodi.

Everything in this package runs under plain CPython, so the tests in
clients/kodi/tests exercise it without a Kodi install: the HTTP client and its
token handling, the X-Client-Capabilities header, the play plan, the list
entries and the artwork proxy. The Kodi glue (resources/lib/kodi) turns their
output into ListItems, dialogs and player calls.
"""
