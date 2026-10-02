// LG Content Store screenshots: the built app (build/index.html, as the TV
// loads it, from file://) in desktop Chrome at 1920x1080, signed in to a
// server, on five screens. The app is a web app, so Chrome draws what the
// TV does; the screenshots come out of the browser rather than an HDMI
// capture (webOS has no screen capture, and the TV's video plane captures
// black). The server takes the file:// origin as it does the TV's
// (internal/api/middleware/cors.go).
//
//   npm run build
//   ONSCREEN_PASSWORD=... npm run screenshots
//
// Environment:
//   ONSCREEN_PASSWORD  the account's password (required; never written to disk)
//   ONSCREEN_SERVER    default https://onscreen-beta.wolverscreen.com
//   ONSCREEN_USER      default reviewer
//   SHOT_FILM          the film for the detail and playback shots (default Sintel)
//   SHOT_ALBUM         the album for the music shot (default: the Music
//                      library's first)
//   SHOT_SEEK_STEPS    10 s steps (→) into the film before its shot (default 9)
//   SHOT_OUT           default ../../docs/store-assets/lg-content-store/screenshots
//   CHROME_PATH        a Chrome / Chromium binary (default: the installed Chrome)
//
// Use an account with nothing but openly licensed content (the review
// server's `reviewer`): store screenshots can't show commercial titles. The
// playback shots leave watch progress on the account; the script clears the
// film's and the track's Continue Watching entries when it's done.

import { existsSync, mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright-core';

const root = join(import.meta.dirname, '..');
const server = (process.env.ONSCREEN_SERVER ?? 'https://onscreen-beta.wolverscreen.com').replace(/\/$/, '');
const username = process.env.ONSCREEN_USER ?? 'reviewer';
const password = process.env.ONSCREEN_PASSWORD;
const filmTitle = process.env.SHOT_FILM ?? 'Sintel';
const albumTitle = process.env.SHOT_ALBUM ?? '';
const seekSteps = Number(process.env.SHOT_SEEK_STEPS ?? 9);
const out = resolve(root, process.env.SHOT_OUT ?? '../../docs/store-assets/lg-content-store/screenshots');
const indexHtml = join(root, 'build', 'index.html');

if (!password) {
  console.error('Set ONSCREEN_PASSWORD (and ONSCREEN_USER / ONSCREEN_SERVER if not reviewer on beta).');
  process.exit(1);
}
if (!existsSync(indexHtml)) {
  console.error('No build/index.html: run `npm run build` first.');
  process.exit(1);
}

// ── The server, from Node ───────────────────────────────────────────────

async function call(method, path, token, body) {
  const resp = await fetch(server + path, {
    method,
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!resp.ok) throw new Error(`${method} ${path}: HTTP ${resp.status}`);
  if (resp.status === 204) return null;
  const j = await resp.json();
  return j?.data ?? j;
}

const pair = await call('POST', '/api/v1/auth/login', null, { username, password });
if (pair.totp_required) throw new Error(`${username} has two-factor sign-in: use an account without it.`);
const token = pair.access_token;
const get = (path) => call('GET', path, token);

const libraries = await get('/api/v1/libraries');
const byType = (type, name) =>
  libraries.find((l) => l.name.toLowerCase() === name) ?? libraries.find((l) => l.type === type);
const movies = byType('movie', 'movies');
const music = byType('music', 'music');
if (!movies) throw new Error(`${username} sees no movie library`);
if (!music) throw new Error(`${username} sees no music library`);

const found = await get(`/api/v1/search?q=${encodeURIComponent(filmTitle)}&limit=10`);
const film = found.find((r) => r.type === 'movie' && r.title.toLowerCase() === filmTitle.toLowerCase()) ??
  found.find((r) => r.type === 'movie');
if (!film) throw new Error(`no film "${filmTitle}" for ${username}`);

// The album: the Music library lists artists (their children are albums)
// or albums.
async function findAlbum() {
  const top = await get(`/api/v1/libraries/${music.id}/items?sort=title&sort_dir=asc&limit=200&offset=0`);
  const albums = [];
  for (const it of top) {
    if (it.type === 'album') albums.push(it);
    else if (it.type === 'artist') albums.push(...(await get(`/api/v1/items/${it.id}/children`)).filter((c) => c.type === 'album'));
    if (!albumTitle && albums.length) break;
  }
  const album = albumTitle
    ? albums.find((a) => a.title.toLowerCase() === albumTitle.toLowerCase())
    : albums[0];
  if (!album) throw new Error(albumTitle ? `no album "${albumTitle}"` : 'no album in the Music library');
  const tracks = (await get(`/api/v1/items/${album.id}/children`)).filter((c) => c.type === 'track');
  if (!tracks.length) throw new Error(`album "${album.title}" has no tracks`);
  return { album, track: tracks[0] };
}
const { album, track } = await findAlbum();

console.log(`server ${server} as ${username}: film "${film.title}", album "${album.title}"`);

// ── The app, in Chrome ──────────────────────────────────────────────────

const browser = await chromium.launch(
  process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : { channel: 'chrome' },
);
const context = await browser.newContext({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
const page = await context.newPage();

// Signed in as lib/api/client's setTokens leaves it, before the app's own
// code runs; once only, so the app's own token refreshes stand.
await page.addInitScript(
  ({ origin, pair }) => {
    if (localStorage.getItem('onscreen.access_token')) return;
    localStorage.setItem('onscreen.api_origin', origin);
    localStorage.setItem('onscreen.access_token', pair.access_token);
    localStorage.setItem('onscreen.refresh_token', pair.refresh_token);
    if (pair.asset_token) {
      localStorage.setItem('onscreen.asset_token', pair.asset_token);
      localStorage.setItem('onscreen.asset_token_at', String(Date.now()));
    }
    localStorage.setItem(
      'onscreen.user',
      JSON.stringify({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin }),
    );
  },
  { origin: server, pair },
);

const appUrl = pathToFileURL(indexHtml).href;
const pause = (ms) => page.waitForTimeout(ms);

/** The app on `route`, its requests and images in, the focus ring settled. */
async function open(route) {
  await page.goto(`${appUrl}${route}`);
  await page.waitForLoadState('networkidle').catch(() => {});
  await page
    .waitForFunction(() => [...document.images].every((i) => i.complete), null, { timeout: 15_000 })
    .catch(() => {});
  await pause(1500);
}

async function shot(name) {
  mkdirSync(out, { recursive: true });
  const file = join(out, `${name}.png`);
  await page.screenshot({ path: file });
  console.log(`  ${file}`);
}

/** Playing, at least `seconds` in. */
async function playing(seconds) {
  await page.waitForFunction(
    (s) => {
      const m = document.querySelector('video, audio');
      return !!m && !m.paused && m.readyState >= 3 && m.currentTime >= s;
    },
    seconds,
    { timeout: 60_000 },
  );
}

try {
  await open('#/hub');
  await shot('01-home');

  await open(`#/item/${film.id}`);
  await shot('02-detail');

  await open(`#/library/${movies.id}`);
  await shot('03-library');

  await open(`#/watch/${film.id}`);
  await playing(1);
  for (let i = 0; i < seekSteps; i++) {
    await page.keyboard.press('ArrowRight');
    await pause(150);
  }
  await pause(1000);
  await playing(1);
  await pause(3000);
  // Down brings the controls up without seeking or pausing.
  await page.keyboard.press('ArrowDown');
  await pause(800);
  await shot('04-playback');

  await open(`#/watch/${track.id}`);
  await playing(3);
  await pause(2000);
  await shot('05-music');

  // Leave the player, so it reports the stop, then clear what the shots
  // left in Continue Watching.
  await open('#/hub');
} finally {
  await browser.close();
  for (const id of [film.id, track.id]) {
    await call('POST', `/api/v1/items/${id}/dismiss-continue-watching`, token, {}).catch(() => {});
  }
}
