# Subtitles for foreign audio

> **Status: decided 2026-11-24; server + web built in #312** (folded in
> before it merged, so migration 00066 writes `foreign` once), **the TV web
> apps (Tizen, webOS, Xbox, so also Steam Deck TV mode) in #314, Android TV
> and the phone in #315** (with their remux / transcode now starting on the
> preferred audio language, which §2 assumed), **Roku and Kodi in the
> follow-up PR** (Roku: a Settings row "Show subtitles: With foreign audio /
> Only forced / Always", its sessions also starting on the preferred audio
> track, and its audio menu now switching a direct play's track; Kodi: the
> mode from the account, and in a direct play the service re-picks when the
> audio is switched in Kodi's own menu). Every §6 recommendation was taken.
> QA and beta run #312 + #314 since 2026-11-25; on QA the web player picked
> as §2 says for foreign-only audio, English audio with a forced track, and
> two dual-audio films switched both ways. Every app now follows §2; older
> app versions read `foreign` as forced-only until they update.

## 1. Why

#312 first made every account default to English audio and English
subtitles with "Forced subtitles only" on. An English film then plays
without subtitles, which is the point. But a film with **no English audio**
(a Japanese film, a French series) and only a **full** English subtitle
track also started with no subtitles: forced-only allows forced tracks
only. Before #312, the file's default-flagged track usually covered that
case.

What people expect is Plex's "Shown with foreign audio" (Jellyfin calls it
"Smart"): **subtitles stay off when the audio is in your subtitle language,
and come on when it isn't.**

## 2. The rule

The apps already choose the audio track before playback: the preferred
language if the file has it, else the file's first track. This rule adds
that track's language as an input to the subtitle pick:

```
pick(subs, prefLang, mode, audioLang):
  no prefLang          → the track the file flags default (forced modes: only if forced)   [unchanged]
  inLang  = subs in prefLang the app can show
  forced  = first forced in inLang
  full    = first non-forced in inLang, non-SDH before SDH
  mode "always"        → full ?? forced
  mode "forced"        → forced                                   [today's forced-only]
  mode "foreign"       → if audioLang is known and ≠ prefLang: full ?? forced
                         else: forced
```

| Audio | Subtitle tracks | "forced" (today) | **"foreign"** |
|---|---|---|---|
| English | English full + English forced | forced | forced |
| English | English full only | off | off |
| Japanese | English full + English forced | forced | **full** |
| Japanese | English full only | off | **full** |
| Japanese | Spanish only | off | off |
| untagged (`und` / empty) | English full | off | off (§6c) |

"Can show" keeps the rules each app has today. An image-based track (PGS)
on a remux or transcode plays its OCR'd text version if it has one, and is
**never burned in automatically**: a burn-in forces a full transcode. Direct
play can pick PGS where the player draws it natively (the Android apps).

The pick runs once at the start of playback, and again when the viewer
switches the audio track, unless they've picked a subtitle themselves in
that session (§6d).

## 3. The setting

`forced_subtitles_only` (a boolean) can't hold three modes. Add a
**`subtitle_mode`** column (`'always' | 'forced' | 'foreign'`, CHECK
constraint) and keep the boolean for apps that haven't updated:

- **GET /users/me/preferences** returns both: `subtitle_mode` plus a
  derived `forced_subtitles_only = (subtitle_mode <> 'always')`. An old
  app sees "foreign" as forced-only, which is exactly what it does today,
  so nothing gets worse before its update ships.
- **PUT /users/me/quality-profile** accepts `subtitle_mode`. An old app's
  PUT carries only the boolean: `false` → `always`; `true` keeps
  `foreign` or `forced` if already set, else `forced`. So an old app
  saving its stream limits can't wipe a new app's "foreign".
- **The migration** adds the column. If folded into #312 (§6a), the backfill
  sets `foreign` instead of forced-only, and the default for new accounts
  is `foreign`.
- User export, OpenAPI and ERROR_CODES.md follow.

The settings screens offer the three modes ("Always", "Only forced",
"When the audio isn't your language") instead of the forced-only switch.

## 4. Work, per app

| App | Code that picks | Setting | Reaches users | Size |
|---|---|---|---|---|
| Server | n/a | column, GET/PUT, export, OpenAPI | server deploy | S |
| Web (+ desktop) | `subtitle-select.ts` `pickPreferredSubtitle`; the watch page picks audio *after* subtitles today, so the order flips; re-pick on audio switch | `LanguagePrefs` checkbox → 3-way select | server deploy (desktop: its next release) | S |
| Xbox (+ Steam Deck TV mode) | `subtitleSelect.ts` + watch page | Settings toggle → 3-way | **server deploy** (`/tvapp/` is served by the server) | S* |
| Samsung (Tizen), LG (webOS) | same files as Xbox, copied | same | store release | S* |
| Android TV / Fire TV | `forcedSubtitleChoice` / `applyPreferredTracks`; audio language from the chosen ordinal or Media3's selected track | Settings | store release | M |
| Phone / Quest | `SubtitleTracks.forcedPreferred` → mode-aware pick; `PlayerScreen` | Playback settings | store release | M |
| Roku | `maybeAutoApplyPreferredSubtitle`; audio language from `m.file.audio_streams[audioPos]` | `SettingsScene` | store release | M |
| Kodi | `tracks.preferred_subtitle` / `player.py` | none (the server's) | repo publish | S |

\* The three TV web apps share the code by copy, so a change is made three
times, each with its own tests and en/es/zh-Hans strings.

**Estimate:** about 4 sessions of code (server + web + TV web apps; the two
Android apps together, per the sibling rule; Roku + Kodi), plus 1 session
of device checks. Store releases follow each platform's normal cadence.
Until a platform's update ships, its users get today's forced-only
behaviour.

## 5. Tests

One table of cases, the rows of §2 plus SDH ordering, "always", and no
preference, written once per app's test suite (web, three TV web apps,
Android TV, phone, Roku, Kodi). Server: the GET derivation, the old-client
PUT rule, and the migration's backfill.

Device checks need a test file with Japanese audio and both a full and a
forced English track, and an English film with a forced track. Run them on
the Firestick or Hisense, S24 FE, Roku Stick, LG C1, the Samsung Q80B, and
the web.

## 6. Decisions

a. **Fold into #312 before it merges (recommended).** The backfill then
   writes `foreign` once, and the web, Xbox and Steam Deck get the whole
   behaviour with the server deploy. If #312 merges first, a later
   migration can only move `forced_subtitles_only = true` accounts to
   `foreign`. That would sweep in the few people who switched forced-only
   on deliberately.
b. **Default mode `foreign`** for new accounts and the backfill
   (recommended), not `forced`.
c. **Untagged audio counts as not foreign** (recommended). Many English
   files have no language tag, and treating those as foreign would put
   subtitles on them.
d. **An audio switch re-picks subtitles** unless the viewer chose one this
   session (recommended).
e. **"Always" picks the full track before the forced one** (recommended).
   Today the web, TV web apps, Roku and Kodi pick forced first, and Android
   picks full (Media3's order). Someone who asked for subtitles always
   wants the full track, and this makes every app agree.
