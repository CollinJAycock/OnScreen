# Google Play beta testers: recruiting kit

## Why this matters

Google Play won't let a new personal developer account publish to production
until **12 testers have been opted in to a closed testing track for 14 days in
a row**. Testers on the *internal* track don't count. Until then, both
Android apps stay off the public store: Android TV / Google TV (TV builds,
currently 26 / 1.5.0) and the phone + Android Auto app (1000+ builds,
currently 1010 / 0.4.2).

Both apps share one listing, `tv.onscreen.android`. A tester who opts in once
gets the phone build on phones and the TV build on Android TV / Google TV
devices (the TV build requires leanback, so Play only offers it to TVs). Fire
TV is not involved: it ships through Amazon.

The clock counts **testers who stay opted in**. Recruit 15–20 so that a few
dropping out doesn't reset the count. Ask testers not to leave the program
before production opens.

## One-time setup (owner, in Play Console and Google Groups)

1. **Create a Google Group** for testers: done 2026-11-05, `onscreenbetatesters@googlegroups.com`
   (groups.google.com > Create group). Set **Who can join** to *Anyone on the
   web can ask* or *Anyone can join*. An open group means people opt in
   without you adding each email by hand.
2. **Add the group to the closed track:** Play Console > OnScreen > Test and
   release > Testing > **Closed testing** > the track testers already use >
   **Testers** > *Google Groups* > add the group's address > Save.
3. **Copy the opt-in link** from the same Testers page ("How testers join your
   test" > *Join on the web*). It is normally
   `https://play.google.com/apps/testing/tv.onscreen.android`. Testers must
   join the group **before** the link works for them.
4. Check the count weekly: Play Console > Closed testing > Testers shows how
   many have opted in. The production-access page shows progress toward the
   12 / 14 days.

## Who to ask

The app needs an **OnScreen Media Server** to talk to. A tester without one
sees only the "Add your server" screen, and Google's reviewers look at whether
testers actually used the app. So recruit people who self-host:

- r/selfhosted, r/homelab, r/Plex, r/jellyfin and r/AndroidTV (check each
  subreddit's rules on self-promotion first; some want a [Release] or
  weekly-thread post).
- The project's GitHub Discussions / README, and on-screen.tv.
- Friends and family who already use your server (they count, and they have
  a server: yours).

**Optional, owner decision:** for testers without a server, a separate,
read-only demo account on onscreen-beta.wolverscreen.com (the openly licensed
review library). Don't hand out `reviewer`: Amazon, LG and Meta's reviewers
use it, and its watch state shows in their tests.

## Invite post

Short (Discord, Mastodon, a comment):

```
OnScreen is a free, open-source media server you run yourself (AGPL-3.0),
and its Android apps need 12 beta testers to get onto Google Play. If you
self-host and have an Android phone or Android TV / Google TV:

1. Join the tester group: https://groups.google.com/g/onscreenbetatesters
2. Opt in: https://play.google.com/apps/testing/tv.onscreen.android
3. Install OnScreen from Play and point it at your OnScreen server.

Please stay opted in for at least two weeks; that's Google's rule.
More: https://on-screen.tv
```

Longer (Reddit, a forum):

```
Title: OnScreen, a self-hosted media server: looking for Android beta testers

OnScreen is an open-source (AGPL-3.0) media server for movies, TV, music,
audiobooks, podcasts and photos, built on Postgres, with hardware
transcoding and apps for Android TV, Fire TV, LG, Samsung, Roku, Xbox,
Meta Quest, Windows/Linux and the web. Server and setup: https://on-screen.tv

Google Play requires 12 testers on a closed track for 14 days before the
Android apps can go public, so I'm looking for testers:

- Android phone app (with Android Auto): library browsing, downloads,
  Chromecast, Watchlist, background audio.
- Android TV / Google TV app: the same library on the big screen, with
  frame-rate matching, HDR / Dolby Vision where your TV supports it, and
  Live TV if your server has a tuner.

You need your own OnScreen server (Docker image and installers on the
site). To join:
1. Join the tester group: https://groups.google.com/g/onscreenbetatesters
2. Opt in on Google Play: https://play.google.com/apps/testing/tv.onscreen.android
3. Install OnScreen from the Play Store on your phone and/or Android TV.

Please stay in the program for at least 14 days. Bug reports welcome on
GitHub: https://github.com/CollinJAycock/OnScreen/issues
```

Fill in `https://groups.google.com/g/onscreenbetatesters` (the group's page, e.g.
`https://groups.google.com/g/onscreen-beta`) and `https://play.google.com/apps/testing/tv.onscreen.android` before
posting.

## Tester-exchange posts (r/AndroidClosedTesting, r/TestersCommunity)

These subreddits exist for Google's 12-tester rule: developers test each
other's apps for 14 days. Read each one's rules first; most want a flair or
title format and expect you to **test other people's apps in return**. Keep
your side of the swap: Google asks about tester engagement when you apply for
production, and testers who never open the app don't help.

Title: `[Tester swap] OnScreen — media player for Android phone & Android TV / Google TV`

```
Looking for testers for OnScreen, an open-source media player (phone,
Android Auto and Android TV / Google TV). I'll test your app back for the
full 14 days; reply with your link.

To join:
1. Join the group: https://groups.google.com/g/onscreenbetatesters
2. Opt in: https://play.google.com/apps/testing/tv.onscreen.android
3. Install OnScreen from the Play Store.

No media server? Reply or DM and I'll send a login for a demo server with
free, openly licensed movies, music and audiobooks, so there's something
to play.

Please stay opted in for 14 days. Thanks!
```

Comment reply (when someone says "tested yours, test mine"):

```
Done — joined your test and installed it, I'll keep it for the 14 days.
Thanks for testing OnScreen!
```

**Demo logins:** create one account per tester on the demo server
(onscreen-beta), never the `reviewer` account the store reviewers share.
A non-admin account without the request permission is enough.

## Steps to send testers

1. Sign in to Google with the account your Android device uses.
2. Join the group: https://groups.google.com/g/onscreenbetatesters
3. Open the opt-in link and choose **Become a tester**: https://play.google.com/apps/testing/tv.onscreen.android
   The page should then say "You're a tester". If it says "internal
   tester", that's the wrong link.
4. Install **OnScreen** from the Play Store link on that page, or search the
   Play Store on your phone or Android TV after a few minutes.
5. Open it, enter your server's address and sign in.

Updates arrive through Play like any app. To leave later, use the same
opt-in page, but please stay at least 14 days.

## Website section (on-screen.tv), once the links exist

A short block under the Apps tiles; the two "Google Play testing" tiles can
link to it.

```html
<p class="note" id="android-beta">Android beta: join the tester group
(<a href="https://groups.google.com/g/onscreenbetatesters">Google Group</a>), then
<a href="https://play.google.com/apps/testing/tv.onscreen.android">opt in on Google Play</a> and install OnScreen on your
phone or Android TV. Please stay in for at least 14 days; it's how the apps
get onto the public store.</p>
```
