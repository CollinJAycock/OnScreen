# qaseed — sample QA media libraries from free content

`qaseed` populates an OnScreen server with **sample QA libraries** built from
free, legally-clear content (Creative Commons / royalty-free) so a reviewer or
QA tester can sign in and immediately **browse and play real titles**.

It exists to clear the Amazon Appstore *"provide test credentials / the reviewer
can't get past login"* rejection: seed a reachable server with this tool, then
hand the reviewer the QA account it creates.

## Why it works offline

The OnScreen scanner derives **titles from the on-disk filename/folder layout**
and **playback from ffprobe** — TMDB/TVDB enrichment and artwork are strictly
optional. So well-named free files yield a browsable, playable library with **no
external metadata API keys**. (Set `TMDB_API_KEY` on the server later if you want
summaries/genres/posters.)

## What it seeds (default manifest)

| Library | Type | Content (CC BY 3.0 / royalty-free) |
|---|---|---|
| QA Movies | `movie` | Big Buck Bunny (2008), Elephants Dream (2006), Sintel (2010), Tears of Steel (2012) — Blender open movies |
| QA Shows | `show` | "OnScreen Sample Series" S01E01–E03 — Google royalty-free sample clips arranged as a series |

Sources are downloaded from Google's long-lived public sample bucket; full
attributions are written to `<media-root>/CREDITS.txt`. Edit the `manifest` var
in `main.go` to add/replace content (e.g. a `music` or `photo` library).

## Usage

```bash
# preview the plan — no network, no server calls
go run ./test/qaseed -dry-run -qa-user qa_reviewer

# seed a server end-to-end
go run ./test/qaseed \
  -server   https://onscreen.example.com \
  -admin-user admin -admin-pass '••••••' \
  -media-root /var/onscreen/qa-media \
  -qa-user  qa_reviewer -qa-pass 'QAPass123!'
```

On success it prints the testing credentials to paste into the Amazon
"Testing Instructions" field.

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `-server` | `http://localhost:7070` | OnScreen base URL |
| `-admin-user` / `-admin-pass` | `admin` / — | admin login (creates libraries) |
| `-token` | — | use an existing admin access token instead of logging in |
| `-bootstrap` | `false` | register `-admin-user` as the first admin (only works on an empty server) |
| `-media-root` | `qa-media` | where content is downloaded (**must be server-readable**) |
| `-qa-user` / `-qa-pass` | — | create a non-admin QA/reviewer account |
| `-restrict` | `true` | scope `-qa-user` to **only** the seeded demo libraries (replaces its entire library-access set) |
| `-download` / `-libs` / `-scan` | `true` | toggle individual steps |
| `-dry-run` | `false` | print the plan only |

## Isolating the test user to demo data

The server is **default-deny**: a non-admin user sees *only* libraries with an
explicit `library_access` row (`Service.ListForUser`). So scoping the QA/reviewer
account to demo content is two parts:

1. **On disk** — put the demo content in a **dedicated dataset/folder**, separate
   from your real media (e.g. a `…/onscreen-demo` dataset with `movies/` and
   `shows/` under it), and mount that folder into the OnScreen server/container.
   The library `scan_paths` must be the path the **server** sees (the in-container
   mount), which is also `-media-root` when the seeder runs on the server host.
2. **In the app** — with `-restrict` (default on) the seeder calls
   `PUT /users/{id}/libraries` to **replace** the QA user's access set with exactly
   the demo libraries. This also strips any library auto-granted at user creation,
   so the QA account cannot see your real libraries.

Keep the QA account **non-admin** (`is_admin=false`) — admins bypass all library
ACLs and would see everything.

## Important caveats

- **`-media-root` must be on a filesystem the server can read** — library
  `scan_paths` are resolved server-side. Run this on the server host, or point
  `-media-root` at a path/share the server sees.
- **Disable TOTP/2FA on the QA account** — otherwise the TV login flow stops at
  the second-factor step and a reviewer can't proceed.
- Re-running is **idempotent**: existing downloads are skipped and a library of
  the same name is reused rather than duplicated.
- Scans run in the background; libraries populate once each scan completes.

## Scanner naming conventions (for extending the manifest)

- **Movies:** `Title (Year)/Title (Year).mp4` (also `.mkv .m4v .avi .mov .ts`)
- **Shows:** `Show/Season 01/Show S01E01 - Title.mkv` (also `1x03` form; anime `Show - 01.mkv`)
- **Music:** ID3/Vorbis **tag**-driven — `Artist/Album/01 Track.mp3` (`.flac .m4a .opus …`)
- **Photos:** images under any folders — `.jpg .png .heic …` (EXIF date used)

Optional local sidecars `poster.jpg` / `fanart.jpg` next to an item give artwork
with no external fetch.
