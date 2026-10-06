# Cutting a release

How a server release (`vX.Y.Z`) is prepared, tagged and published. Client
store releases (Fire TV, Google Play, Samsung, LG) follow their own notes in
each client's directory; this file is about the server tag.

## 1. Prep commit on main

One commit, `chore(release): prep the vX.Y.Z cut`:

- **CHANGELOG.md** — the `[vX.Y.Z] — unreleased` section gets an intro
  paragraph and an **Upgrade notes** block (migrations in the range, anything
  an operator must do or will notice: new defaults, new required settings,
  sign-outs, rescans) and a **Security** subsection. Patch releases cut from a
  release branch since the last minor must already have their sections on
  main (see [Patch releases](#patch-releases)).
- **Docs re-snapshot** — README status line and client list,
  `docs/deployment.md` (migration table, env vars, upgrade section),
  `docs/security.md` (retitle the "unreleased" section),
  `docs/api/openapi.yaml` `info.version`, `docs/comparison-matrix.md`
  addendum, ARCHITECTURE Known Issues.
- **Version** — `VERSION` and the desktop app (`clients/desktop/package.json`,
  `src-tauri/Cargo.toml`, `tauri.conf.json`) match the tag;
  `desktop-client.yml` warns when they don't. The server binary takes its
  version from the tag (`-X main.version`), not from `VERSION`.
- **Assets** — nothing bundled or in `screenshots/` shows commercial titles'
  artwork.

## 2. Gates before tagging

- `main` is green on `ci.yml` (unit, integration, UAT) and the latest
  `e2e-nightly` run.
- `npm ci && npm run build` succeeds in `web/` and `clients/xbox/` (the
  release workflow runs `npm ci`; a stale lockfile fails the release).
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` reports nothing
  reachable, and `npm audit --omit=dev` is clean in both web apps.
- A local `docker build -f docker/Dockerfile .` succeeds.
- **Upgrade rehearsal** — bring up the previous release's image on a scratch
  compose stack, add users, libraries (movie, show, music, audiobook), watch
  progress and favorites, record what the API returns, then switch to an
  image built from the release commit with `up -d --force-recreate migrate
  server worker`. Migrations must apply cleanly and the same data, plus
  existing sign-ins, must still be there.
- QA deployed from the release commit and smoke-tested.
- The hardware and manual checks in
  [docs/v2.1-release-test-plan.md](docs/v2.1-release-test-plan.md) that the
  release touches.
- A release that breaks the API ships only after the first-party store
  builds that handle it are live (see [docs/server-lock.md](docs/server-lock.md)).

## 3. Tag

On tag day, a second commit stamps the date:
`## [vX.Y.Z] — unreleased` becomes `## [vX.Y.Z] — YYYY-MM-DD`
(`chore(release): vX.Y.Z — stamp release date`). Then an annotated tag on
that commit:

```sh
git tag -a vX.Y.Z -m "OnScreen vX.Y.Z" -m "<two-line summary>"
git push origin vX.Y.Z
```

`desktop-vX.Y.Z` tags belong to the desktop app's own builds; don't reuse
them for the server.

## 4. What the tag push does

- **`release.yml`** runs the Go tests, builds the web app and the TV app,
  then the server binaries (Linux amd64/arm64 server and worker, macOS
  amd64/arm64, Windows), checksums, and the multi-arch image
  `ghcr.io/collinjaycock/onscreen` tagged `X.Y.Z`, `X.Y` and `X`. Binaries and
  the image get SLSA provenance attestations
  (`gh attestation verify <file> --repo CollinJAycock/OnScreen`). A tag
  containing `-` is published as a prerelease.
- **`desktop-client.yml`** builds the Windows and Linux desktop installers
  and attaches them to the same GitHub Release.
- Not automated: the server's Windows and Linux installers (`installer/`) are
  built by hand and attached afterwards.
- The release build doesn't inject `DEFAULT_TMDB_KEY` /
  `DEFAULT_OPENSUBTITLES_KEY` (only the Makefile and `Dockerfile.gpu` do), so
  GitHub binaries and the ghcr image ship without bundled keys; admins enter
  their own in Settings.

## Patch releases

Branch `release/X.Y.Z` from the tag being patched, cherry-pick the fixes,
add a `[vX.Y.Z]` CHANGELOG section there, tag from that branch, and **port
the CHANGELOG section back to main** in the same sitting (v2.4.1's was
missed and restored in the v2.5.0 prep). Fixes land on main first unless
main has moved on so far that they can't.
