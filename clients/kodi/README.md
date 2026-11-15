# OnScreen for Kodi

`plugin.video.onscreen` is a Kodi add-on for an OnScreen server. You browse the
server's libraries in Kodi's own interface and play through Kodi's player. The
server stays the record of what you watched. Resume points, Continue Watching,
Next Up and watched marks match the other OnScreen apps.

- **Kodi 20 (Nexus) or later**, any platform. Tested on Kodi 21.3 for Windows.
- **OnScreen server v2.6 or later.** On servers newer than v2.7, a remux of an
  indexed MKV comes as one seekable playlist.

It's a browsing add-on. It doesn't sync anything into Kodi's own library. That
keeps the server the single source of truth, so there's nothing to rescan.

## What it does

- **Home rows:** Continue Watching, Next Up, Recently Added and Trending.
- **Libraries:** movies, shows (show → season → episode), anime and cartoons,
  home videos, music (artist → album → track), audiobooks and podcasts. Video
  libraries appear when the add-on is opened from Kodi's Videos section, audio
  libraries from Music. A video library opens on All, Recently added and
  Unwatched (movies add In progress). A music library opens on All (artists),
  Albums and Recently added; an audiobook library on All (authors) and Books.
  A podcast library lists its podcasts.
- **Photos:** photo libraries in Kodi's Pictures section, newest first. A
  photo opens in Kodi's picture viewer and slideshow.
- **Live TV:** the server's channels, with what's on now and next, and its
  recordings (a finished recording plays like any title). Shown when the
  server has Live TV.
- **Lists:** Watchlist (Plan to Watch), Favorites, promoted collections, and
  search.
- **Context menu:** mark watched or unwatched (a season or show marks every
  episode), add to or remove from the Watchlist and Favorites, remove from
  Continue Watching, go to the show, and *Play with options…*. Options asks
  for the version (when there are several), the audio track and the
  subtitles, including a picture subtitle burned into a transcode.
- **Up Next:** with the Up Next add-on installed (*Add-ons → Search → Up
  Next*, from Kodi's repository; Kodi lists it as optional when installing
  this add-on but doesn't install it), the next episode is announced near the
  end of one and plays on its own.
- **"Play on…":** this Kodi shows up in the other OnScreen apps' "Play on"
  list once it has played something, and starts what they send it.
- **Sign in** with a PIN or with a password. For a PIN, open `<server>/pair` on
  a phone or computer that's signed in (single sign-on works there too). A
  password sign-in asks for a two-factor code when the account has one.
  *Sign out* revokes this device on the server.
- **Interface** in English, Spanish and Simplified Chinese, following Kodi's
  language. The server's own messages, such as "too many streams", follow it
  too.

## How playback works

1. The add-on asks the server for its play decision
   (`POST /items/{id}/playback-decision`). It sends an `X-Client-Capabilities`
   header saying what this Kodi plays (`resources/lib/onscreen/caps.py`).
   Kodi's player is ffmpeg, so the header is wide: every container the server
   indexes, every audio codec (Kodi downmixes itself), and H.264, HEVC, AV1,
   VP9 and MPEG-2 video. The server direct-plays almost everything.
2. **Direct play** streams the file from `/media/stream/{file}` with its
   file-bound stream token. Kodi sees every audio and subtitle track in the
   file, and its own track pickers work.
3. Otherwise the server runs a **remux or transcode session** and Kodi plays
   its HLS playlist. Reasons include a bitrate cap for a viewer outside the
   home network, the account's quality profile, a resolution above the
   *Highest resolution* setting, or HDR on a screen without it. Sessions use
   MPEG-TS segments. When Kodi passes Dolby through to a receiver, the server
   copies AC-3 / E-AC-3 tracks and, on a fleet that can, turns TrueHD or DTS
   into E-AC-3 rather than AAC.
4. **Resume.** When a title has a saved position and a known length, Kodi
   asks "Resume from …" itself. Otherwise the add-on asks. "Play on…" starts
   where the other app was.
5. **Tracks.** The account's preferred audio and subtitle languages (set in
   the web app) pick the tracks, as on the other apps: a direct play switches
   Kodi's player to them once it starts, and a remux or transcode asks the
   server for that audio track. *Play with options…* asks instead.
6. The add-on's **background service** reports progress to the server every 10
   seconds while playing, and on pause, resume, seek and stop. The stop report
   moves Continue Watching and marks the title played past 90%. The service
   also deletes the remux or transcode session afterwards, so its ffmpeg stops
   right away. If an admin stops the stream, or a parental watch limit is
   reached, the next report is refused and the service stops the player with
   the reason.

**Seeking in a transcode.** A transcode's playlist grows while the server
encodes it. Kodi's built-in HLS player treats a growing playlist as live and
can't seek in it. With **InputStream Adaptive** installed, the add-on plays
such sessions through it. Playback then starts at the first segment, and you
can seek anywhere the server has already encoded. The first time a growing
transcode starts without it, the add-on asks Kodi to install it (once;
*Settings → Advanced* brings the offer back). Android builds usually include
it. A
remux of an indexed MKV, or a server with the adaptive bitrate ladder on
(`TRANSCODE_ABR=true`), gives the whole timeline up front, so either player
seeks anywhere.

**Subtitles.** A direct play has all of the file's embedded subtitles. The
add-on downloads the server's external subtitles (sidecar files and
OpenSubtitles downloads) for every playback. For a remux or transcode it also
downloads up to four embedded text tracks: the chosen one, then forced ones
and your preferred language. A picture subtitle (PGS, VobSub) can't become a
file; picked in *Play with options…* for a remux or transcode, the server
burns it into the picture (a full transcode). If the server can't, the title
plays without it and says why.

**Artwork.** Server artwork needs a credential. The only credential that fits
in a URL lasts 24 hours, and Kodi caches images by URL, so it would download
every poster again each day. Instead, the service runs a small artwork proxy
on `127.0.0.1:52872` (*Settings → Advanced*). Kodi sees stable URLs, and the
proxy adds the current token on its way to the server. Photos go through it
too (`/photo/<id>.jpg`). It answers loopback requests for those two paths
only. If the proxy is off or its port is taken, images load straight from
the server with the 24-hour token.

## Settings

| Setting | Default | What it does |
|---|---|---|
| Server address | (asked on first sign-in) | `http://192.168.1.10:7070`, `https://media.example.com`. *Change server…* checks a new address before saving it; typed without `https://` or `http://` it tries https first. Plain `http://` is for servers on the local network only (private and loopback addresses, `.local` / `.lan` names, single-label names); to any other host the add-on refuses it. |
| Highest resolution | 4K | The server scales anything larger down to 1080p or 720p. |
| Play HEVC / AV1 / VP9 | on | Turn one off when this device stutters on it. The server then converts that codec. |
| HDR video | Automatic | Automatic follows what the display reports (`System.SupportedHDRTypes`). *Play as HDR* lets Kodi tone-map. *Have the server convert it to SDR* asks the server to. |
| Dolby Vision | Automatic | Claims profiles 5, 8.1 and 8.4 when the display reports Dolby Vision. |
| Ask which version to play | off | Otherwise the best version plays. *Play with options…* in the context menu always asks. |
| Receive "Play on…" | on | This Kodi asks the server every 5 seconds (30 once idle for 10 minutes) for a title another app sent it. |
| Offer InputStream Adaptive | on | Turns itself off after the one offer. |
| Items per page | 200 | Library pages end with *Next page*. |
| Artwork proxy / port | on / 52872 | See above. |

## Install

**From the OnScreen repository** (Kodi then keeps the add-on up to date): turn
on *Settings → System → Add-ons → Unknown sources*, use *Add-ons → Install
from zip file* on `repository.onscreen-1.0.0.zip`, then *Install from
repository → OnScreen Add-ons → Video add-ons → OnScreen*.

**Or from the add-on's own zip** (no updates): *Install from zip file* on
`plugin.video.onscreen-<version>.zip`.

CI builds both as artifacts on every change (`plugin.video.onscreen` and
`kodi-repository`). To build them yourself:

```sh
python clients/kodi/scripts/package.py      # -> clients/kodi/dist/plugin.video.onscreen-<version>.zip
python clients/kodi/scripts/build_repo.py   # -> clients/kodi/dist/repo/
```

### The repository

`repository.onscreen` reads `https://on-screen.tv/kodi/addons.xml`, checks
it against `addons.xml.md5`, and checks each add-on zip against its
`.sha256`. `scripts/build_repo.py` writes that whole tree; publishing a
release is copying it to the website's `kodi/` folder and deploying the
site:

```sh
python clients/kodi/scripts/build_repo.py --out site/kodi
```

Bump the version in `plugin.video.onscreen/addon.xml` (and its `<news>`) for
every release: Kodi updates only to a higher version. To try a repository
before publishing, build it with `--base-url http://127.0.0.1:8099/`, serve
the folder (`python -m http.server 8099 --bind 127.0.0.1`), and install that
build's repository zip. Kodi warns that a plain-HTTP repository is insecure,
which is expected for this local test only; that's how the repository
was checked on Kodi 21.3 (install from it, the sha256 check, updating over a
copied-in version).

Not done: a submission to Kodi's official repository (a pull request to
`xbmc/repo-plugins`, which has its own review).

## Development

```
plugin.video.onscreen/
  addon.xml, default.py (plugin), service.py (service)
  resources/settings.xml, resources/language/*/strings.po (generated)
  resources/lib/onscreen/   no Kodi imports: api, caps, playback, tracks, listing, upnext, artproxy
  resources/lib/kodi/       the Kodi glue: plugin router, player, service, auth, list items
repository.onscreen/        the repository add-on
scripts/strings.py          the string table and .po generator
scripts/package.py          the zip
scripts/build_repo.py       the repository tree
tests/                      pytest; tests/kodistubs stand in for Kodi's modules
```

```sh
cd clients/kodi
pip install pytest ruff
python -m pytest -q          # unit tests + the plugin and service against a fake server
ruff check . && ruff format --check .
python scripts/strings.py    # after editing the string table
```

- **Strings** live in `scripts/strings.py`, one row per string with its
  English, Spanish and Chinese text. Edit the table, then run the script. The
  tests fail when a `.po` file is stale, a translation's `{placeholders}`
  differ from the English, or the code uses a string id that isn't in the
  table. The Spanish and Chinese follow the web app's catalog wording and
  are drafts until a fluent reader reviews them, like the rest of v3's
  translations.
- **Kodi's API.** The stubs accept any name, so `tests/test_kodi_api.py`
  checks every `xbmc*` name, InfoTag setter and Kodi method the add-on uses
  against `tests/kodi21_api.json`. That file is `dir()` of the real modules,
  dumped from Kodi 21.3. Regenerate it from a newer Kodi with a throwaway
  script add-on that dumps `dir()` of each module and class.
- **Token refresh.** Kodi runs the plugin and the service in separate Python
  interpreters, and the server ends a whole sign-in when a refresh token is
  presented twice. Refreshes therefore run under a file lock, re-reading the
  stored token first (`onscreen/api.py`).

### Running it against a real Kodi

The add-on was checked this way on Kodi 21.3 for Windows, against a local
compose stack (`docker/docker-compose.yml`) with a fixture admin and
synthetic media. Everything below goes through Kodi's JSON-RPC.

1. Before Kodi's first start, put these in `userdata/guisettings.xml`:
   `services.esenabled` = `true` (JSON-RPC on `127.0.0.1:9090`) and
   `services.esallinterfaces` = `false`. For a windowed Kodi, also set
   `videoscreen.screenmode` = `WINDOW`. Set `debug.screenshotpath` to a folder
   for screenshots.
2. Unzip the add-on into `addons/`, start Kodi, then call
   `Addons.SetAddonEnabled`. Kodi disables add-ons that were copied in.
   Call `Application.SetMute` too.
3. Drive it with `GUI.ActivateWindow` (with a `plugin://` path),
   `Files.GetDirectory` (to list a plugin folder), `Input.*`, `Input.SendText`
   (keyboards), `Player.Open`, `Player.GetProperties` and
   `Input.ExecuteAction` `screenshot`. Read the PIN off a screenshot and claim
   it with `POST /api/v1/auth/pair/claim`.
4. The service and the plugin log to `kodi.log` with the prefix
   `[plugin.video.onscreen]`. Each play logs one line with the chosen plan and
   start position.

## Known gaps

- **Changing tracks mid-way through a transcode.** A remux or transcode
  carries one audio track, so another one means stopping and using *Play with
  options…* (direct play has Kodi's own pickers).
- **E-books** stay in the other apps: Kodi has no reader.
- **Live TV playback** was checked as far as the add-on goes (channels, guide
  text, the stream it asks for, the same URL the web player uses), but not on
  a real tuner: the local test server had none that its IPTV ingest takes.
  DVR scheduling isn't in the add-on; recordings are.
