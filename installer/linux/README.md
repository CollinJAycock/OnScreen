# OnScreen — Linux portable build

Version: `<VERSION>`
Built: `<BUILD_TIME>`

This tarball is a **portable test deployment** of OnScreen for Linux x86_64.
It is not a polished installer — it's the smallest viable bundle that
gets the server running on a fresh box so you can validate transcode
behavior on real hardware (NVIDIA NVENC, Intel VAAPI/QSV, AMD VAAPI).

## What's in the tarball

| File | Purpose |
| ---- | ------- |
| `server` | Main HTTP API + embedded web UI (static, CGO-disabled binary — runs on glibc 2.17+ and musl) |
| `worker` | Transcode worker (runs in-process by default; this binary is for the multi-host fan-out path) |
| `devtoken` | Issues a dev JWT for smoke-testing |
| `onscreen.service` | systemd unit template (rendered into `/etc/systemd/system/` by `install-service.sh`) |
| `ffmpeg/ffmpeg` + `ffprobe` | John Van Sickle static build — has NVENC, **VAAPI**, libsvtav1, libdav1d |
| `start.sh` | Foreground launch (interactive use, Ctrl+C to stop) |
| `install-service.sh` | Register systemd unit + enable at boot |
| `uninstall-service.sh` | Remove the unit (leaves files in place) |
| `docker-compose.deps.yml` + `start-deps.sh` | Postgres + Valkey via Docker Compose |
| `.env.example` | Config template — copy to `.env` and edit |

## Quickstart

### 1. Prereqs

- **Linux x86_64**, glibc 2.17+ or musl — no other system libs required
  for the server itself.
- **Docker Engine 20.10+** with the `compose` v2 plugin (or legacy
  `docker-compose`) for the dependencies (Postgres + Valkey).
  *Alternative:* install Postgres 16 + Valkey/Redis natively if you
  prefer — the server only cares about `DATABASE_URL` and `VALKEY_URL`.
- **GPU drivers** (only if you want hardware transcoding):
  - NVIDIA: proprietary driver + `cuda-runtime` package. The static
    ffmpeg dlopens `libcuda.so.1` + `libnvidia-encode.so.1`.
  - Intel/AMD VAAPI: `libva2` + vendor driver (`intel-media-driver`
    or `mesa-va-drivers`). Verify with `vainfo`.

### 2. Extract and configure

```bash
tar -xzf onscreen-linux-amd64-<VERSION>.tar.gz
cd onscreen-linux-amd64-<VERSION>
cp .env.example .env
${EDITOR:-nano} .env       # SECRET_KEY at minimum; MEDIA_PATH if not /srv/media
```

The location matters for the systemd unit: `install-service.sh` bakes
the absolute path of the install dir into the unit file at install
time, so don't extract under `/tmp/` if you intend to register the
service.

### 3. Bring up Postgres + Valkey

```bash
./start-deps.sh
```

This runs `docker compose -f docker-compose.deps.yml up -d`. Postgres
listens on 5432, Valkey on 6379, and the credentials match what
`.env.example` ships with — no further wiring needed.

### 4. Run

**Foreground (smoke / dev / interactive):**

```bash
./start.sh
# Ctrl+C to stop. Open http://localhost:7070 in a browser.
```

**Background (systemd service, auto-start on boot):**

```bash
sudo ./install-service.sh
# Service runs as your normal user (the SUDO_USER), not root.
# Verify:
systemctl status onscreen
journalctl -u onscreen -f
```

To remove the service later:

```bash
sudo ./uninstall-service.sh
```

The uninstall step only removes the systemd registration. Your install
directory, `.env`, media, and Postgres/Valkey state are untouched.

## Updating

1. Stop the service: `sudo systemctl stop onscreen`.
2. Replace `server` / `worker` / `devtoken` / `ffmpeg/*` with the new
   binaries (preserve `.env` and any data you've put alongside).
3. Start the service: `sudo systemctl start onscreen`.

The Postgres database holds all the state — schema migrations run
automatically at server start. There's no separate "upgrade" step
beyond replacing the binary.

## Hardware transcoding — quick check

After the server is running, hit the encoder-detection log line. With
`LOG_LEVEL=info` (the default) it shows up at startup:

```
{"level":"INFO","msg":"transcode encoders","active":["h264_nvenc","hevc_nvenc","libx264"],"detected":[...]}
```

If your GPU isn't in the `active` list:

- NVIDIA: confirm `nvidia-smi` works under your user. If it does but
  ffmpeg still doesn't see NVENC, check `ffmpeg -hide_banner -encoders | grep nvenc`
  using the bundled `./ffmpeg/ffmpeg`.
- VAAPI: `vainfo` must show `VAEntrypointEncSlice` for H.264 / HEVC.
  Add the run user to the `video` group: `sudo usermod -aG video $USER`,
  then re-login.

## Troubleshooting

- **"Operation not permitted" on systemd start with VAAPI**:
  `onscreen.service` ships with `NoNewPrivileges=true` + `PrivateTmp=true`
  + `ProtectSystem=full`. If your VAAPI driver needs broader access,
  loosen those after `install-service.sh` writes the unit file (edit
  `/etc/systemd/system/onscreen.service` and `systemctl daemon-reload`).

- **Cannot read `/srv/media`**: the unit runs as your invoking user.
  Either move media under their `$HOME`, or `chmod`/`chown` the media
  tree so they can read it. The default unit has `ProtectHome=read-only`
  which lets the user read their own home but blocks writes there —
  fine for media browsing.

- **Container ffmpeg vs bundled ffmpeg**: the bundled one is in
  `./ffmpeg/`. The systemd unit prepends that to `PATH` so it wins
  over any system install. If you'd rather use a system ffmpeg, edit
  the `Environment="PATH=…"` line in the unit file.
