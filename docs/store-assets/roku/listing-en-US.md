# Roku Streaming Store listing (English, United States)

Roku Developer Dashboard → Apps → Add app. Free, no ads, no in-app
purchases. The fields below follow Roku's channel publishing guide; the
limits are Roku's.

## Create app

| Field | Value |
|---|---|
| App name (max 30) | OnScreen |
| Countries | Every country offered (the app ships no content of its own) |
| Default language | English |

## Listing setup

| Field | Value |
|---|---|
| Made for kids? | No |
| Category | The dashboard's closest to a personal media player (Plex and Jellyfin list as video / personal media) |
| App age rating | The lowest the questionnaire allows: the app has no content of its own |

## Store assets (English)

**App description, on device (max 300)** — 278 characters:

> Play the movies, shows, music, audiobooks and photos on your own OnScreen Media Server, the free open-source server you run on your own computer or NAS. OnScreen includes no content: connect it to your server. Resume anywhere, 4K HDR10, subtitles, chapters. No ads, no tracking.

**App description, online (max 1,500)** — 1,453 characters:

> OnScreen plays the media library on your own OnScreen Media Server, free open-source software you install on your own computer or NAS. The app includes no movies, shows, music or channels: connect it to your server to browse and play the videos, music, audiobooks and photos you have added there.
>
> To use this app you need your own OnScreen Media Server set up and running, with media you have added to it. OnScreen does not provide, sell, host or link to any movies, shows, music or TV channels. It works only with your OnScreen server. Please add only media you own or have the right to use.
>
> - Home with Continue Watching, Next Up and each library's recently added, in the order you set
> - Movies, TV shows, music, audiobooks and photos, with search, sorting and filters
> - 4K and HDR10 on TVs that support them; Dolby Digital and Dolby Digital Plus to your sound system
> - Resume where you left off, shared with your other OnScreen apps
> - Audio tracks, subtitles, chapters, and Skip Intro / Skip Credits where your server has marked them
> - Music and audiobooks with a now-playing screen and audiobook speed; photo slideshows
> - Send what you're watching on your phone or computer to your Roku with "Play on"
>
> Getting started: install OnScreen Media Server (github.com/CollinJAycock/OnScreen), add your media, then open this app, enter your server's address and sign in.
>
> No ads, no analytics and no tracking. The app talks only to the server you choose.

| Asset | File |
|---|---|
| App poster (540×405) | `app-poster-540x405.png` (the same image as the manifest's `mm_icon_focus_fhd`) |
| Screenshots (up to 6, 1920×1080) | `screenshots/01-home.jpg` … `06-photo.jpg`: Home (recently added rows), the Movies grid, Tears of Steel's page, Caminandes' page, music now playing (Kevin MacLeod, CC BY 4.0), a photo (Earthrise, NASA). Taken on the Roku against the review server; open-licence demo media only |

## App profile

| Field | Value |
|---|---|
| App type | Video |
| Privacy policy URL | https://onscreen.wolverscreen.com/privacy |
| Terms URL | (none) |
| Do customers sign in? | Yes (the dashboard requires Yes for free apps with a sign-in) |
| Customer support URL | https://github.com/CollinJAycock/OnScreen/issues |
| Customer support email / phone | The owner's |
| Learn more URL | https://on-screen.tv |
| Administrative / technical contacts | The owner's |

## Monetization

Free ("I will not be monetizing my app").

## Test credentials (for Roku's review)

The dashboard's authentication types are subscription / cable ones; pick
the closest (SVOD) and explain in the description:

| Field | Value |
|---|---|
| Credentials title | OnScreen review server |
| Description | "OnScreen needs an OnScreen Media Server. A test server with open-licence demo media (Blender open movies, Creative Commons music) is at https://onscreen-beta.wolverscreen.com. On first launch enter that address, then sign in with the username and password below. The app shows only the media on that server and has no store, purchases or ads." |
| Username | `reviewer` |
| Password | The review account's password (entered in the dashboard only; never stored in the repo) |
| Valid until | At least a year out |

## Deep linking

One set per media type the app handles. Content IDs are OnScreen item
ids (UUIDs) on the review server; the reviewer's account must be able
to see them.

| Media type | Content | Content ID |
|---|---|---|
| movie | Tears of Steel | `246dd9f5-4a39-46c8-a713-88859fd2b875` |
| episode | Caminandes S1·E1, Llama Drama | `0e6a7f1e-5485-4cd8-9b9c-59e6e77518fd` |
| series | Caminandes | `9f5e52a8-6738-4d14-8f85-384b3ed3139f` |

All three checked on the Roku signed in as `reviewer` (2026-11-04): the
movie and the episode start playing, the series opens its page.

`movie` and `episode` start playback; `series` and `season` open the
title's page (`source/util/DeepLink.brs`).

## Package

A signed `.pkg` made with the Roku's Packager (`genkey` over Telnet on
port 8080, then Utilities → Packager on the device's developer page)
from the `1.0.0` build. Keep the genkey password and Dev ID: every
update must be signed with the same key. Minimum firmware: 8.0 (squashfs).

## Release notes (max 1,500)

1.0.0, 964 characters:

```
OnScreen 1.0.0 — first release on Roku.

Watch and listen to the media library on your own OnScreen Media Server, the free open-source server you run on your own computer or NAS.

• Home with Continue Watching, Next Up and each library's recently added, in the order you set on the web
• Movies, TV shows, music, audiobooks and photos, with search, sorting and filters
• 4K and HDR10 on TVs that support them; Dolby Digital and Dolby Digital Plus passed to your sound system
• Resume where you left off, shared with your other OnScreen apps
• Audio tracks, subtitles, chapters, and Skip Intro / Skip Credits where your server has marked them
• Music and audiobooks with a now-playing screen and audiobook speed; photo slideshows
• Send what you're watching on your phone or computer to your Roku with "Play on"
• Sign in with your username and password, or from your phone with a PIN

No ads, no analytics, no tracking. The app talks only to the server you choose.
```

## Schedule publish

Earliest is the second business day after approval; not Friday or the
weekend.
