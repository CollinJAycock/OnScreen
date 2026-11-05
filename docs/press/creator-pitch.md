# Pitching OnScreen to video creators

A short email, sent from the maintainer's own address, works better than a
long one. Creators get many pitches: lead with why their viewers would care,
link the press kit (https://on-screen.tv/press.html), offer a demo login, and
don't follow up more than once. Lead with features and architecture, not AI.

**Timing:** best when the Android apps are in Google Play production and the
TV store reviews have cleared, so viewers can install everything the day a
video goes up. Mentioning the Android beta in an earlier pitch is fine.

## The email

Subject: `A free, open-source media server with apps on almost every TV — for your channel?`

```
Hi <name>,

I make OnScreen, a free, open-source (AGPL-3.0) media server, and I think it
might suit <channel> because <one line from the list below>.

What makes it different:
- No paid tier at all: hardware transcoding, remote streaming, Live TV/DVR
  and single sign-on are all in the free server.
- Its own apps on nearly every platform: web, Windows/Linux, Android phone
  with Android Auto, Android TV/Google TV, Fire TV, LG, Samsung, Roku, Xbox
  and Meta Quest (several are in store review right now).
- Dolby Vision profile 7 is converted to 8.1 on the fly, so single-layer
  Dolby Vision TVs actually show Dolby Vision.
- Bit-perfect music (gapless, ReplayGain, hi-res, DSD), audiobooks,
  podcasts, books and photos in the same server.
- Built on Go, PostgreSQL and Valkey; extra machines can join as GPU
  transcode workers.

Press kit with screenshots and facts: https://on-screen.tv/press.html
Source: https://github.com/CollinJAycock/OnScreen

If you'd like to try it before deciding, I can set you up with a login on
a demo server, so there's nothing to install. Happy to answer anything.

Thanks,
Collin Aycock
```

## A first line per channel

Pick the one that fits and replace `<one line …>`. Check the channel's
recent uploads first and mention a specific one if you can: that matters
more than anything else in the email.

| Channel | Opening line |
|---|---|
| DB Tech | "your single-app walk-throughs are exactly how people find new self-hosted tools, and OnScreen has a lot to walk through." |
| Awesome Open Source | "it's a fully open-source media server with no paid tier, which seems right in your lane." |
| Techno Tim | "it would slot into a homelab services tour as a Plex alternative, and it's built on Postgres and Valkey like the rest of a modern stack." |
| Jim's Garage | "it deploys with Docker Compose in a few lines, and the GPU transcode workers are a fun homelab extra." |
| Christian Lempa | "it's a self-hosted media server with OIDC, SAML and LDAP in the free version, which I haven't seen elsewhere." |
| Spaceinvader One | "there's an Unraid template for it now, and a lot of Unraid users are looking for a Plex alternative." |
| IBRACORP | "there's an Unraid template for it, and the single sign-on and GPU transcoding fit your guides." |
| TechHut | "it's a Jellyfin-style server written in Go on Postgres, with apps on almost every TV platform." |

## After a yes

- Create a demo-server login for them (one per creator, never `reviewer`).
- Point them at the Unraid template (`docker/unraid/`) or the Docker quick
  start in the README.
- Offer to answer questions on a call or by email before they record.
