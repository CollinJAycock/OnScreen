<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { afterNavigate, goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    api,
    endpoints,
    Unauthorized,
    type ItemDetail,
    type ChildItem,
    type IssueKind,
    type UpNext
  } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import {
    episodeFocusKey,
    focusFirstOf,
    restoreGuard,
    restoreKeyed,
    seasonOfEpisodeKey,
    takeFocusMemo,
    type FocusMemo,
    type RestoreGuard,
  } from '$lib/focus/memory';
  import Spinner from '$lib/components/Spinner.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import OptionsDialog from '$lib/components/OptionsDialog.svelte';
  import ReportProblemDialog from '$lib/components/ReportProblemDialog.svelte';
  import { goBack, openChild as openChildNav, playItem } from '$lib/nav';
  import {
    childWatchBadge,
    endpointMissing,
    isMarkableLeaf,
    isWatchContainer,
    markAllOptions,
    upNextLabel,
    watchWriteError
  } from '$lib/watchState';
  import { canReportProblem, openKinds } from '$lib/reportProblem';
  import { pickContainerStart, resolvePlayAll, resolveShuffle } from '$lib/playPick';
  import { formatRuntime, genreLine, resumeLabel } from '$lib/detailText';
  import { latestOnly } from '$lib/latest';

  // Back from the player: how long to wait before reading the item again
  // (see afterNavigate below).
  const RETURN_REFRESH_MS = 1500;

  let item = $state<ItemDetail | null>(null);
  let children = $state<ChildItem[]>([]);
  let error = $state('');

  // Show context: when viewing a show, season, or episode, render
  // the full show hierarchy (season picker + selected-season's
  // episodes) so the user can browse without drilling. showSeasons
  // is the show's seasons; seasonEpisodes is the episodes for the
  // currently-selected season.
  let showSeasons = $state<ChildItem[]>([]);
  let selectedSeasonId = $state<string | null>(null);
  let seasonEpisodes = $state<ChildItem[]>([]);
  let seasonEpisodesLoading = $state(false);
  // Whose episodes seasonEpisodes holds. It can trail selectedSeasonId: a
  // mark's refresh supersedes the season pick's read, and if that refresh
  // fails the grid would show the previous season under the new chip.
  // State: the episode cards' focus keys name it (see episodeFocusKey).
  let episodesOf = $state<string | null>(null);
  // The selected season's newest read failed and left the grid empty: say
  // so, and the season's chip reads it again (see selectSeason).
  let episodesFailed = $state(false);

  // Up-next (v2.5, shows + seasons): which episode Play starts, its label
  // ("Resume S3 · E4" / "Play S3 · E5" / "Watch again") and which "Mark
  // all" buttons make sense. upNextLoaded doubles as "this server has the
  // watch-state routes" for the container pages; older servers 404 and the
  // page keeps its pre-v2.5 Play button without any mark controls.
  let upNext = $state<UpNext | null>(null);
  let upNextLoaded = $state(false);
  // Actions render once the container's up-next attempt settled, so the
  // Play button doesn't flip from "Play S1E1" to "Resume S3 · E4".
  let actionsReady = $state(false);

  // Watched marks.
  let marking = $state(false);
  let confirmUnwatchShow = $state(false);
  let episodeMenu = $state<ChildItem | null>(null);
  let menuOrigin: HTMLElement | null = null;
  let status = $state('');
  let statusTimer: ReturnType<typeof setTimeout> | null = null;

  // Report a problem (v2.5): offered once GET /items/{id}/issues answers, so
  // an older server (404) never shows the button.
  let reportAvailable = $state(false);
  let reportKinds = $state<Set<IssueKind>>(new Set());
  let reportOpen = $state(false);

  // Favorite (♥). Optimistic: flips at once, flips back with a message if
  // the server refuses. Seeded from the item's is_favorite.
  let isFavorite = $state(false);
  let favoriteBusy = false;

  // Artist pages: albums and loose music videos (v2.0 music_video) as two
  // tabs, like Android's Albums / Music Videos tabs.
  let artistTab = $state<'albums' | 'videos'>('albums');
  // Play All / Shuffle need a request or two to find their track.
  let resolving = $state(false);

  // Back to this page (from a child page or the player): the hero's first
  // button and the grid's first card hold their autofocus while the element
  // that left the page takes focus again (lib/focus/memory).
  let restoring = $state(false);

  // Cleared on destroy: an async Play All that resolves after Back must not
  // yank the user into the player from wherever they went.
  let alive = true;
  let returnRefreshTimer: ReturnType<typeof setTimeout> | null = null;
  // The first load: the item, its children and up-next, then the show
  // context (for a show, the opening season's episodes). Settles, never
  // rejects. The return refresh waits for it (see refreshAfterPlayback).
  let initialLoad: Promise<void> = Promise.resolve();

  // Re-reads overlap: the first load, the return-from-player refresh and a
  // mark's refresh all read the item, its children, up-next and the season's
  // episodes. Each kind is numbered and only the newest request's answer (or
  // failure) is applied, so an older GET landing last can't undo newer state
  // ("Mark watched" flipping back to unwatched).
  const itemReads = latestOnly();
  const childReads = latestOnly();
  const upNextReads = latestOnly();
  const episodeReads = latestOnly();

  const itemId = $derived(page.params.id!);
  const fanartUrl = $derived(
    item?.fanart_path ? api.assetUrl(`/artwork/${item.fanart_path}?w=1920`) : ''
  );
  const posterUrl = $derived(
    item?.poster_path ? api.assetUrl(`/artwork/${item.poster_path}?w=480`) : ''
  );

  // Artist children: albums (plus anything that isn't a music video, so a
  // future child type still shows up) and the artist's music videos.
  const artistAlbums = $derived(
    item?.type === 'artist' ? children.filter((c) => c.type !== 'music_video') : [],
  );
  const artistVideos = $derived(
    item?.type === 'artist' ? children.filter((c) => c.type === 'music_video') : [],
  );
  // Play All / Shuffle start from an album's tracks; without an album there
  // is nothing for them to start.
  const artistHasAlbums = $derived(artistAlbums.some((c) => c.type === 'album'));
  // The grid under the hero: the selected artist tab, else every child.
  const gridChildren = $derived.by<ChildItem[]>(() => {
    if (item?.type !== 'artist') return children;
    return artistTab === 'videos' || artistAlbums.length === 0 ? artistVideos : artistAlbums;
  });

  // Meta line, worded like Android's: "2h 5m" and up to three genres.
  const runtime = $derived(formatRuntime(item?.duration_ms));
  const genres = $derived(genreLine(item?.genres));

  // book_author + book_series have no playable file of their own and
  // we deliberately hide the "Play first child" button because the
  // first child is itself a parent (a series under an author, a book
  // under a series). An artist with no albums has no Play All either.
  // The first hero button would then be Favorite, which isn't where the
  // user wants to start — so autofocus the first grid card instead.
  const autofocusGridFirstCard = $derived(
    !!item && item.files.length === 0 &&
      (item.type === 'book_author' ||
        item.type === 'book_series' ||
        (item.type === 'artist' && !artistHasAlbums && children.length > 0))
  );

  const upNextText = $derived(upNextLabel(upNext));
  // Container pages on a v2.5 server drive Play + Mark all from up-next.
  const containerWatch = $derived(!!item && isWatchContainer(item.type) && upNextLoaded);
  // Movies / episodes carry their own watch_state on v2.5 servers.
  const leafWatch = $derived(!!item && isMarkableLeaf(item.type) && item.watch_state !== undefined);
  const markAll = $derived(markAllOptions(upNext));
  // Hold-OK options on episode cards: this server can mark (see above).
  const episodeMarks = $derived(containerWatch || (item?.type === 'episode' && leafWatch));

  interface Action {
    key: string;
    label: string;
    primary?: boolean;
    onclick: () => void;
  }

  // The hero's buttons, in order. The first one takes focus on load.
  const actions = $derived.by<Action[]>(() => {
    if (!item || !actionsReady || item.type === 'book') return [];
    const out: Action[] = [];
    const it = item;
    if (it.files.length > 0) {
      // A playable item (movie, episode, track, single-file audiobook…).
      // Part-watched: "Resume from 1:02:03" plus "Play from Beginning".
      // Play and Resume pass NO start: the player reads the position fresh,
      // while this page's copy can be one step behind right after the player
      // closed (its 'stopped' report is still landing; see afterNavigate).
      // Only "Play from Beginning" names a start, the top.
      const resumeMs = it.view_offset_ms || 0;
      if (resumeMs > 0) {
        out.push({
          key: 'play',
          label: resumeLabel(resumeMs),
          primary: true,
          onclick: () => playItem(it.id),
        });
        out.push({ key: 'play-start', label: 'Play from Beginning', onclick: () => playItem(it.id, 0) });
      } else {
        out.push({ key: 'play', label: 'Play', primary: true, onclick: () => playItem(it.id) });
      }
    } else if (containerWatch) {
      const ep = upNext?.episode;
      const rewatch = upNext?.mode === 'rewatch';
      if (upNextText && ep) {
        // Only "Watch again" names a start, the top: the finished episode's
        // last position (95 % in) must not be resumed. "Resume S3 · E4" and
        // "Play S3 · E5" pass none and the player reads the episode's position
        // fresh, as Resume above: this page's up-next can still say "Play"
        // for an episode the player was closed partway through moments ago.
        out.push({
          key: 'play',
          label: upNextText,
          primary: true,
          onclick: () => playItem(ep.id, rewatch ? 0 : undefined),
        });
      }
    } else if (it.type === 'show' && seasonEpisodes.length > 0) {
      // Older server (no up-next): Play drills two layers down to a real
      // playable episode. children[0] is a SEASON (no file), so we use the
      // selected season's first episode instead.
      out.push({
        key: 'play',
        label: `Play S${seasonEpisodes[0].index ?? 1}E1`,
        primary: true,
        onclick: () => playChild(seasonEpisodes[0].id),
      });
    } else if (it.type === 'artist') {
      // Play All starts the first track of the oldest album and the
      // player's auto-advance carries on through the rest; Shuffle starts a
      // random track and continues in order from there (Android parity).
      if (artistHasAlbums) {
        out.push({ key: 'play', label: 'Play All', primary: true, onclick: () => void playArtist('all') });
        out.push({ key: 'shuffle', label: 'Shuffle', onclick: () => void playArtist('shuffle') });
      }
    } else if (children.length > 0 && it.type !== 'book_author' && it.type !== 'book_series') {
      // Container types (season / album / podcast / multi-file
      // audiobook) whose children are playable: Play picks the part-played
      // one, else the first not played yet, else the first. book_author +
      // book_series are pure browse parents — the first child is itself
      // a parent (series under an author, multi-file book under a
      // series), so Play would land on a non-playable row. Hide and let
      // the user pick a book from the grid below.
      out.push({ key: 'play', label: 'Play', primary: true, onclick: playContainer });
    }
    if (leafWatch) {
      const watched = it.watch_state === 'watched';
      out.push({
        key: 'mark',
        label: watched ? 'Mark unwatched' : 'Mark watched',
        onclick: () => void markItem(!watched),
      });
    } else if (containerWatch) {
      if (markAll.watched) {
        out.push({ key: 'mark-all', label: 'Mark all watched', onclick: () => void markContainer(true) });
      }
      if (markAll.unwatched) {
        out.push({
          key: 'unmark-all',
          label: 'Mark all unwatched',
          onclick: () => {
            // Unwatching a whole show wipes every mark + resume point: confirm.
            if (it.type === 'show') {
              menuOrigin = focusManager.currentElement();
              confirmUnwatchShow = true;
            } else {
              void markContainer(false);
            }
          },
        });
      }
    }
    out.push({
      key: 'favorite',
      label: isFavorite ? '♥ Favorited' : '♥ Favorite',
      onclick: () => void toggleFavorite(),
    });
    if (reportAvailable) {
      out.push({
        key: 'report',
        label: 'Report a problem',
        onclick: () => {
          menuOrigin = focusManager.currentElement();
          reportOpen = true;
        },
      });
    }
    return out;
  });

  onMount(() => {
    // Taken now, before anything awaits: the note is the route's only while
    // Back is what brought it up.
    const memo = takeFocusMemo();
    // Ends the restore when the page goes away or the user moves focus first.
    const guard = restoreGuard();
    restoring = !!memo?.focusedId;
    initialLoad = (async () => {
      try {
        // Every read here goes through the same sequences as the refreshes,
        // so a refresh can't be overtaken by a slow first answer (or the
        // other way round).
        await itemReads(endpoints.items.get(itemId), (fresh) => (item = fresh));
        const it = item;
        if (!it) return;
        isFavorite = !!it.is_favorite;
        if (canReportProblem(it.type)) void loadReportState();
        // Container types load children. "audiobook" is dual-shape:
        // single-file books have files of their own (children empty),
        // multi-file books expose audiobook_chapter children.
        // book_author + book_series are the audiobook hierarchy
        // parents above an audiobook row — drilling into either
        // renders the children list (series + standalone books for
        // an author, books for a series). "artist" is a music library's
        // top level: its children are albums and music videos.
        if (
          it.type === 'show' ||
          it.type === 'season' ||
          it.type === 'artist' ||
          it.type === 'album' ||
          it.type === 'podcast' ||
          it.type === 'audiobook' ||
          it.type === 'book_author' ||
          it.type === 'book_series'
        ) {
          // Shows + seasons ask for up-next alongside their children so the
          // page can open on the up-next episode's season.
          await Promise.all([
            childReads(endpoints.items.children(itemId), (raw) => {
              children = it.type === 'book_author' ? authorOrder(raw) : raw;
            }),
            isWatchContainer(it.type) ? loadUpNext() : Promise.resolve(),
          ]);
        }
        actionsReady = true;

        // Show context (show/season/episode): assemble the show's
        // season list + selected season's episodes so the page renders
        // the full hierarchy instead of just one layer.
        if (it.type === 'show') {
          showSeasons = children;
          // Open on the season Play will start in (up-next), else the first.
          const upSeason = upNext?.episode?.season_id;
          const start = children.find((c) => c.id === upSeason) ?? children[0];
          // Awaited: the opening season's episodes are part of the first load.
          if (start) await selectSeason(start.id);
        } else if (it.type === 'season') {
          // children = this season's episodes (already loaded above).
          // Seed seasonEpisodes from it and fetch sibling seasons via
          // the show (parent_id).
          seasonEpisodes = children;
          selectedSeasonId = it.id;
          episodesOf = it.id;
          if (it.parent_id) {
            try {
              showSeasons = await endpoints.items.children(it.parent_id);
            } catch { showSeasons = []; }
          }
        } else if (it.type === 'episode' && it.parent_id) {
          // Walk up to the season → the show. Two extra round-trips
          // for the show id; tolerable since episode pages are a
          // deep-link surface (Continue Watching tile, search hit).
          try {
            const season = await endpoints.items.get(it.parent_id);
            const showId = season.parent_id;
            selectedSeasonId = season.id;
            const [seasons] = await Promise.all([
              showId ? endpoints.items.children(showId) : Promise.resolve([] as ChildItem[]),
              readEpisodes(season.id),
            ]);
            showSeasons = seasons;
          } catch {
            // Best-effort; an episode page with hero + Play still works.
          }
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();
    if (memo && restoring) {
      void initialLoad
        .then(() => restorePageFocus(memo, guard))
        .finally(() => (restoring = false));
    }

    const offBack = focusManager.pushBack(() => {
      goBack();
      return true;
    });
    return () => {
      alive = false;
      guard.end();
      offBack();
      if (statusTimer) clearTimeout(statusTimer);
      if (returnRefreshTimer) clearTimeout(returnRefreshTimer);
    };
  });

  // Back from the player. The 'stopped' report carrying the final position
  // goes out as the player closes and can land after this page's first GET,
  // which would leave "Resume from …" (and the tracks' / episodes' progress)
  // one step behind. Read them again once that report has had time to land.
  // (Android has the player hand its final position straight back instead.)
  afterNavigate((nav) => {
    if (!nav.from?.route.id?.startsWith('/watch') || returnRefreshTimer) return;
    returnRefreshTimer = setTimeout(() => {
      returnRefreshTimer = null;
      void refreshAfterPlayback();
    }, RETURN_REFRESH_MS);
  });

  async function refreshAfterPlayback() {
    // Not before the first load has settled: started earlier, the refresh's
    // reads would supersede the first ones, and the page would pick its
    // opening season from an up-next answer that never got applied (Season
    // 1's grid and "Play S1E1" on a show watched up to S3). And with no item
    // yet there would be nothing to refresh.
    await initialLoad;
    const it = item;
    if (!alive || !it) return;
    const jobs: Promise<unknown>[] = [refreshWatchState()];
    // refreshWatchState re-reads the item only for markable videos; a track
    // or a single-file audiobook has a resume point too.
    if (!isMarkableLeaf(it.type) && it.files.length > 0) jobs.push(readItem());
    // Tracks / chapters / podcast episodes carry the progress Play picks by.
    if (it.type === 'album' || it.type === 'podcast' || it.type === 'audiobook') {
      jobs.push(childReads(endpoints.items.children(itemId), (c) => (children = c)).catch(() => {}));
    }
    await Promise.all(jobs);
    await tick();
    // The focused button can go away ("Play from Beginning" once the item
    // finished): land on the first hero button rather than nowhere.
    if (alive && !focusManager.currentElement()) {
      const first = document.querySelector<HTMLElement>('.actions [data-focusable]');
      if (first) focusManager.focus(first);
      else focusManager.refocus();
    }
  }

  // Back to this page: focus goes back to what left it (a hero button such
  // as "Play from Beginning", an episode, an album) once the first load has
  // rendered it, else to where a fresh page starts. All of it stops once
  // `guard` does (the user moved focus first, or left).
  async function restorePageFocus(memo: FocusMemo, guard: RestoreGuard<HTMLElement>) {
    const key = memo.focusedId;
    if (!alive || !key) return;
    await tick();
    // An episode listed under another season than the one the page opened
    // on (up-next has moved on to the next season): that season first.
    const season = seasonOfEpisodeKey(key);
    if (season && season !== selectedSeasonId && showSeasons.some((s) => s.id === season)) {
      if (!guard.active) return;
      await selectSeason(season);
      await tick();
    }
    // A music video, or the tab itself: the artist's Music Videos tab.
    if (item?.type === 'artist' && (key === 'tab:videos' || artistVideos.some((v) => v.id === key))) {
      artistTab = 'videos';
      await tick();
    }
    if (!alive || restoreKeyed(memo, guard)) return;
    // Gone (e.g. "Play from Beginning" once the item finished): the
    // element a fresh page would have focused.
    focusFirstOf(autofocusGridFirstCard ? '.grid [data-focusable]' : '.actions [data-focusable]', guard);
  }

  /** Re-read the item itself (newest read wins; failures keep what's shown). */
  function readItem(): Promise<unknown> {
    return itemReads(endpoints.items.get(itemId), (fresh) => {
      if (alive) item = fresh;
    }).catch(() => {});
  }

  async function loadUpNext(): Promise<void> {
    try {
      await upNextReads(endpoints.items.upNext(itemId), (u) => {
        upNext = u;
        upNextLoaded = true;
      });
    } catch (e) {
      if (e instanceof Unauthorized) throw e;
      // Older server (404) or a transient failure: keep what the page has —
      // on first load that's the pre-v2.5 Play button and no mark controls.
    }
  }

  // book_author: series alphabetical first, then standalone books
  // year-desc. Mirrors the Android TV + phone bucket ordering so the same
  // browse mental model holds across surfaces.
  function authorOrder(raw: ChildItem[]): ChildItem[] {
    const series = raw
      .filter(c => c.type === 'book_series')
      .sort((a, b) => a.title.localeCompare(b.title));
    const books = raw
      .filter(c => c.type === 'audiobook')
      .sort((a, b) => {
        const ya = a.year ?? -1;
        const yb = b.year ?? -1;
        if (ya !== yb) return yb - ya;
        return a.title.localeCompare(b.title);
      });
    return [...series, ...books];
  }

  async function loadReportState() {
    try {
      const mine = await endpoints.issues.listMine(itemId);
      reportKinds = openKinds(mine);
      reportAvailable = true;
    } catch (e) {
      // 404 = a server without reports: no button. Anything else still
      // offers it (the dialog reports its own errors).
      if (!(e instanceof Unauthorized) && !endpointMissing(e)) reportAvailable = true;
    }
  }

  // Every way into the player goes through nav.playItem (start overrides
  // and the player's back-stack handling live there). No start = resume
  // from the item's own position.
  function playChild(childId: string) {
    playItem(childId);
  }

  function playContainer() {
    const start = pickContainerStart(children);
    // No start for the part-played or first unplayed child: the player reads
    // its position fresh, since this page's children list can be one step
    // behind right after the player closed (like Resume above). Only a replay
    // of a finished container (the picked child is watched) starts at 0.
    if (start) playItem(start.child.id, start.child.watched ? 0 : undefined);
    else showStatus('Nothing to play yet');
  }

  async function playArtist(mode: 'all' | 'shuffle') {
    if (resolving) return;
    resolving = true;
    try {
      const fetchChildren = (id: string) => endpoints.items.children(id);
      const track =
        mode === 'all'
          ? await resolvePlayAll(children, fetchChildren)
          : await resolveShuffle(children, fetchChildren);
      if (!alive) return;
      if (track) playItem(track.id, 0);
      else showStatus('Nothing to play yet');
    } catch (e) {
      if (!alive) return;
      if (e instanceof Unauthorized) goto('#/login');
      else showStatus("Couldn't start playback.");
    } finally {
      resolving = false;
    }
  }

  async function toggleFavorite() {
    const it = item;
    if (!it || favoriteBusy) return;
    const was = isFavorite;
    favoriteBusy = true;
    isFavorite = !was;
    try {
      if (was) await endpoints.items.removeFavorite(it.id);
      else await endpoints.items.addFavorite(it.id);
    } catch (e) {
      isFavorite = was;
      if (e instanceof Unauthorized) goto('#/login');
      else showStatus(watchWriteError(e, "Couldn't update Favorites."));
    } finally {
      favoriteBusy = false;
    }
  }

  function openChild(childId: string) {
    openChildNav(childId);
  }

  /** A season's episodes through the episode sequence: a read of the
   *  previous season (or one from before a mark) that lands late can't
   *  replace newer episodes. Rejects only when it is the newest read. */
  function readEpisodes(seasonId: string, request = endpoints.items.children(seasonId)): Promise<boolean> {
    return episodeReads(request, (eps) => {
      if (selectedSeasonId !== seasonId) return;
      seasonEpisodes = eps;
      episodesOf = seasonId;
      episodesFailed = false;
    });
  }

  /** The newest read for `seasonId` failed while it's the selected season:
   *  an empty grid with a note rather than another season's episodes under
   *  its chip. */
  function episodesReadFailed(seasonId: string) {
    if (selectedSeasonId !== seasonId) return;
    seasonEpisodes = [];
    episodesOf = seasonId;
    episodesFailed = true;
  }

  async function selectSeason(seasonId: string) {
    // The chip whose episodes are showing (or on their way) does nothing.
    // Pressed over an empty grid (its read failed, or a mark's refresh
    // cleared it) it reads again: the active chip is how to retry.
    if (
      selectedSeasonId === seasonId &&
      (seasonEpisodesLoading || (episodesOf === seasonId && seasonEpisodes.length > 0))
    ) {
      return;
    }
    selectedSeasonId = seasonId;
    seasonEpisodesLoading = true;
    try {
      await readEpisodes(seasonId);
    } catch {
      // The newest read failed (an older one failing is ignored).
      episodesReadFailed(seasonId);
    } finally {
      if (selectedSeasonId === seasonId) seasonEpisodesLoading = false;
    }
  }

  function showStatus(text: string) {
    status = text;
    if (statusTimer) clearTimeout(statusTimer);
    statusTimer = setTimeout(() => (status = ''), 4000);
  }

  // Re-read what a mark changed: the item's own state (leaf pages), the
  // up-next answer (containers) and the visible episodes' check marks.
  async function refreshWatchState() {
    const jobs: Promise<unknown>[] = [];
    if (item && isMarkableLeaf(item.type)) jobs.push(readItem());
    if (item && isWatchContainer(item.type)) jobs.push(loadUpNext().catch(() => {}));
    if (selectedSeasonId) {
      const sid = selectedSeasonId;
      const eps = endpoints.items.children(sid);
      jobs.push(
        readEpisodes(sid, eps).catch(() => {
          // Failed, and the newest. This season's list stays (its marks
          // may be a step behind). A list that is still the previous
          // season's (this read superseded the season pick's) goes, as
          // in selectSeason.
          if (episodesOf !== sid) episodesReadFailed(sid);
        }),
      );
      // A season page's own children are that same list.
      if (item?.id === sid) jobs.push(childReads(eps, (list) => (children = list)).catch(() => {}));
    }
    await Promise.all(jobs);
  }

  async function markItem(watched: boolean) {
    if (marking) return;
    marking = true;
    try {
      if (watched) await endpoints.items.markWatched(itemId);
      else await endpoints.items.markUnwatched(itemId);
      await refreshWatchState();
      showStatus(watched ? 'Marked as watched' : 'Marked as unwatched');
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else showStatus(watchWriteError(e, "Couldn't update watched state."));
    } finally {
      marking = false;
    }
  }

  async function markContainer(watched: boolean) {
    confirmUnwatchShow = false;
    if (marking) return;
    // Pressed straight from the hero (no confirm): come back to that button
    // if it survives the change, else restoreFocus picks the first one.
    if (!menuOrigin) menuOrigin = focusManager.currentElement();
    marking = true;
    try {
      if (watched) await endpoints.items.markWatched(itemId);
      else await endpoints.items.markUnwatched(itemId);
      await refreshWatchState();
      showStatus(watched ? 'Marked all as watched' : 'Marked all as unwatched');
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else showStatus(watchWriteError(e, "Couldn't update watched state."));
    } finally {
      marking = false;
      await restoreFocus();
    }
  }

  async function restoreFocus() {
    await tick();
    const origin = menuOrigin;
    menuOrigin = null;
    if (origin && document.body.contains(origin)) {
      focusManager.focus(origin);
      return;
    }
    // The button it came from went away (e.g. "Mark all unwatched" once
    // nothing is watched): land on the first hero button instead.
    const first = document.querySelector<HTMLElement>('.actions [data-focusable]');
    if (first) focusManager.focus(first);
    else focusManager.refocus();
  }

  function openEpisodeMenu(ep: ChildItem) {
    menuOrigin = focusManager.currentElement();
    episodeMenu = ep;
  }

  async function closeEpisodeMenu() {
    episodeMenu = null;
    await restoreFocus();
  }

  async function toggleEpisode(ep: ChildItem) {
    episodeMenu = null;
    const watched = !ep.watched;
    // Optimistic check mark; refreshWatchState() brings the truth. It is
    // newer than any episode read still on its way (an earlier mark's
    // refresh), so those are dropped rather than landing on top of it and
    // taking the mark back.
    episodeReads.supersede();
    seasonEpisodes = seasonEpisodes.map((e) =>
      e.id === ep.id ? { ...e, watched, view_offset_ms: 0 } : e,
    );
    await restoreFocus();
    try {
      if (watched) await endpoints.items.markWatched(ep.id);
      else await endpoints.items.markUnwatched(ep.id);
      showStatus(watched ? 'Marked as watched' : 'Marked as unwatched');
    } catch (e) {
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      // Take the mark back now, so it doesn't stay up if the refresh below
      // fails as well.
      episodeReads.supersede();
      seasonEpisodes = seasonEpisodes.map((x) => (x.id === ep.id ? ep : x));
      showStatus(watchWriteError(e, "Couldn't update watched state."));
    }
    await refreshWatchState();
  }

  async function closeReport(reported?: IssueKind) {
    reportOpen = false;
    if (reported) reportKinds = new Set([...reportKinds, reported]);
    await restoreFocus();
  }

  // Section heading for the children grid. Same labels the Android
  // detail page uses so a user moving between clients sees the same
  // mental model — "Chapters" for audiobooks, "Tracks" for albums,
  // "Episodes" for shows / podcasts.
  function childrenHeading(type: string): string {
    switch (type) {
      case 'album':
        return 'Tracks';
      case 'audiobook':
        return 'Chapters';
      case 'artist':
        return 'Albums';
      case 'book_author':
      case 'book_series':
        return 'Books';
      default:
        return 'Episodes';
    }
  }
</script>

{#if error}
  <p class="error">{error}</p>
{:else if !item}
  <Spinner />
{:else}
  <div class="page">
    {#if fanartUrl}
      <div class="fanart" style="background-image: url({fanartUrl})"></div>
      <div class="fanart-scrim"></div>
    {/if}

    <div class="content">
      <div class="hero">
        {#if posterUrl}
          <img class="hero-poster" src={posterUrl} alt="" />
        {/if}
        <div class="hero-text">
          <h1>{item.title}</h1>
          <div class="meta">
            {#if item.year}<span>{item.year}</span>{/if}
            {#if item.content_rating}<span class="pill">{item.content_rating}</span>{/if}
            {#if runtime}<span>{runtime}</span>{/if}
            {#if item.rating}<span>★ {item.rating.toFixed(1)}</span>{/if}
            {#if genres}<span>{genres}</span>{/if}
            {#if item.watch_state === 'watched'}<span class="watched-pill">Watched</span>{/if}
          </div>
          {#if item.summary}<p class="summary">{item.summary}</p>{/if}

          <div class="actions">
        {#if item.type === 'book'}
          <!-- Ebooks (EPUB / CBZ / CBR) need a paginated reader; the
               TV clients don't ship one. Show a clear message instead
               of routing to /watch, which would stall trying to play
               an archive file as video. -->
          <div class="note">Book reading isn't available on TV. Open this book in the web or phone app.</div>
        {:else}
          <!-- Keyed for focus memory: Back from the player puts focus on
               the button that started it ("Play from Beginning" too). -->
          {#each actions as a, i (a.key)}
            <button
              use:focusable={{ autofocus: i === 0 && !autofocusGridFirstCard && !restoring }}
              class="btn"
              class:primary={a.primary}
              data-focus-key={`action:${a.key}`}
              data-focus-control
              onclick={a.onclick}
              disabled={(marking && a.key !== 'play') || (resolving && (a.key === 'play' || a.key === 'shuffle'))}
            >
              {a.label}
            </button>
          {/each}
        {/if}
          </div>
          {#if status}<div class="status" role="status">{status}</div>{/if}
        </div>
      </div>

      {#if showSeasons.length > 0}
        <!-- Two-tier show layout: season chips along the top, episode
             grid below for the selected season. Rendered for show,
             season, and episode pages alike so the user always sees
             the full hierarchy. -->
        <section class="children">
          <h2>Seasons</h2>
          <!-- data-focus-row: Left / Right stay among the chips. Right
               from the last one used to drop diagonally into the episode
               grid below. -->
          <div class="season-chips" data-focus-row>
            {#each showSeasons as season (season.id)}
              <button
                use:focusable
                class="season-chip"
                class:active={selectedSeasonId === season.id}
                data-focus-key={`season:${season.id}`}
                data-focus-control
                onclick={() => selectSeason(season.id)}
              >
                {season.title}
              </button>
            {/each}
          </div>

          {#if seasonEpisodesLoading}
            <Spinner />
          {:else if episodesFailed && seasonEpisodes.length === 0}
            <p class="note">Couldn't load this season's episodes. Select the season again to retry.</p>
          {:else if seasonEpisodes.length > 0}
            <h2 class="episodes-heading">
              Episodes{#if episodeMarks}<span class="hint">Hold OK on an episode to mark it watched</span>{/if}
            </h2>
            <div class="grid">
              {#each seasonEpisodes as ep (ep.id)}
                {@const badge = childWatchBadge(ep)}
                <PosterCard
                  title={ep.index ? `${ep.index}. ${ep.title}` : ep.title}
                  posterPath={ep.thumb_path ?? ep.poster_path}
                  subtitle={ep.duration_ms ? `${Math.round(ep.duration_ms / 60000)}m` : undefined}
                  progressRatio={badge?.progress ?? undefined}
                  watched={badge?.watched ?? false}
                  focusKey={episodeFocusKey(episodesOf, ep.id)}
                  onclick={() => playChild(ep.id)}
                  onlongpress={episodeMarks ? () => openEpisodeMenu(ep) : undefined}
                />
              {/each}
            </div>
          {/if}
        </section>
      {:else if children.length > 0}
        <section class="children">
          {#if item.type === 'artist' && artistAlbums.length > 0 && artistVideos.length > 0}
            <!-- An artist with both: Albums / Music Videos tabs, styled like
                 the season chips; Left / Right stay among them. -->
            <div class="season-chips" data-focus-row>
              <button
                use:focusable
                class="season-chip"
                class:active={artistTab === 'albums'}
                data-focus-key="tab:albums"
                data-focus-control
                onclick={() => (artistTab = 'albums')}
              >
                Albums
              </button>
              <button
                use:focusable
                class="season-chip"
                class:active={artistTab === 'videos'}
                data-focus-key="tab:videos"
                data-focus-control
                onclick={() => (artistTab = 'videos')}
              >
                Music Videos
              </button>
            </div>
          {:else}
            <h2>{item.type === 'artist' && artistAlbums.length === 0 ? 'Music Videos' : childrenHeading(item.type)}</h2>
          {/if}
          <div class="grid">
            {#each gridChildren as child, i (child.id)}
              {#if child.type === 'episode' || child.type === 'audiobook_chapter' || child.type === 'track' || child.type === 'podcast_episode' || child.type === 'music_video'}
                <!-- No art of its own (an album's tracks, a book's chapters):
                     the parent's cover rather than a blank tile of initials. -->
                <PosterCard
                  title={child.index ? `${child.index}. ${child.title}` : child.title}
                  posterPath={child.thumb_path ?? child.poster_path ?? item.poster_path}
                  subtitle={child.duration_ms ? `${Math.round(child.duration_ms / 60000)}m` : undefined}
                  focusKey={child.id}
                  autofocus={autofocusGridFirstCard && i === 0 && !restoring}
                  onclick={() => playChild(child.id)}
                />
              {:else}
                <PosterCard
                  title={child.title}
                  posterPath={child.poster_path}
                  subtitle={child.year ? String(child.year) : undefined}
                  focusKey={child.id}
                  autofocus={autofocusGridFirstCard && i === 0 && !restoring}
                  onclick={() => openChild(child.id)}
                />
              {/if}
            {/each}
          </div>
        </section>
      {/if}
    </div>
  </div>

  {#if episodeMenu}
    <OptionsDialog
      title={episodeMenu.index ? `${episodeMenu.index}. ${episodeMenu.title}` : episodeMenu.title}
      options={[
        {
          label: episodeMenu.watched ? 'Mark as unwatched' : 'Mark as watched',
          onselect: () => {
            if (episodeMenu) void toggleEpisode(episodeMenu);
          },
        },
      ]}
      oncancel={() => void closeEpisodeMenu()}
    />
  {/if}

  {#if confirmUnwatchShow}
    <OptionsDialog
      title="Mark all unwatched?"
      message={`Mark every episode of "${item.title}" as unwatched? This clears your watched marks and resume points for the whole show.`}
      options={[{ label: 'Mark all unwatched', danger: true, onselect: () => void markContainer(false) }]}
      focusCancel
      oncancel={() => {
        confirmUnwatchShow = false;
        void restoreFocus();
      }}
    />
  {/if}

  {#if reportOpen}
    <ReportProblemDialog
      itemId={item.id}
      itemTitle={item.title}
      fileId={item.files[0]?.id}
      openKinds={reportKinds}
      onclose={(k) => void closeReport(k)}
    />
  {/if}
{/if}

<style>
  .page {
    position: relative;
    min-height: 100%;
  }

  /* No `inset` / flexbox `gap` in this page's CSS: webOS 6 runs Chromium 79
     (inset is Chrome 87, flex gap 84). Spacing comes from margins instead. */
  .fanart {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    background-size: cover;
    background-position: center top;
    filter: brightness(0.4);
    z-index: 0;
  }

  .fanart-scrim {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    background: linear-gradient(180deg, rgba(7,7,13,0.3) 0%, var(--bg-primary) 80%);
    z-index: 0;
  }

  .content {
    position: relative;
    z-index: 1;
    padding: var(--page-pad);
  }

  .hero {
    display: flex;
    align-items: flex-start;
    margin-bottom: 40px;
  }

  .hero-poster {
    width: 320px;
    height: 480px;
    border-radius: 12px;
    object-fit: cover;
    background: var(--bg-elevated);
    flex: 0 0 auto;
    margin-right: 48px;
  }

  .hero-text {
    flex: 1;
    min-width: 0;
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 0 0 20px;
  }

  /* Wrapping rows (.meta, .actions, .season-chips) space their children
     with a top + right margin, and the row pulls itself up and right by the
     same amount: the first line and the line ends then sit exactly where a
     flex `gap` would put them, and the gap between wrapped lines stays. */
  /* Combinators below put the inner part in :global(): Svelte 5 scopes
     every compound after the first as :where(.svelte-xyz), and :where()
     needs Chrome 88, so on webOS 6 (Chromium 79) the whole rule would be
     dropped. :global() leaves that part unscoped, and the rule stays valid
     (the outer class still keeps it to this component). */
  .meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin: -12px -24px 24px 0;
  }

  .meta > :global(span) {
    margin: 12px 24px 0 0;
  }

  .pill {
    border: 2px solid var(--border-strong);
    padding: 2px 12px;
    border-radius: 6px;
    font-size: var(--font-sm);
  }

  .watched-pill {
    color: #34d399;
  }

  .summary {
    font-size: var(--font-md);
    max-width: 1100px;
    color: var(--text-primary);
    line-height: 1.5;
    margin: 0 0 40px;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    margin: -24px -24px 0 0;
  }

  .actions > :global(*) {
    margin: 24px 24px 0 0;
  }

  .status {
    margin-top: 20px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .note {
    padding: 16px 24px;
    border-radius: 10px;
    background: var(--bg-elevated);
    color: var(--text-secondary);
    font-size: var(--font-md);
    max-width: 720px;
  }

  .btn {
    font-family: inherit;
    font-size: var(--font-md);
    padding: 20px 48px;
    border-radius: 12px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }

  .btn.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }

  .btn:disabled {
    opacity: 0.6;
  }

  .children > :global(h2) {
    font-size: var(--font-lg);
    margin: 0 0 24px;
  }

  .episodes-heading {
    margin-top: 48px !important;
  }

  .hint {
    margin-left: 24px;
    font-size: var(--font-xs);
    font-weight: 400;
    color: var(--text-muted);
  }

  .season-chips {
    display: flex;
    flex-wrap: wrap;
    margin: -16px -16px 32px 0;
  }

  .season-chip {
    margin: 16px 16px 0 0;
    font-family: inherit;
    font-size: var(--font-md);
    padding: 12px 28px;
    border-radius: 999px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }

  .season-chip.active {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }

  /* Columns the cards' own width (PosterCard is a fixed 240 px tile), as
     many as fit: four 1fr columns stretched over the page left about
     200 px of empty track beside every card. Rows a little further apart
     than columns, for the titles under the art (the library grid's
     spacing). */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, 240px);
    column-gap: var(--card-gap);
    row-gap: calc(var(--card-gap) + 24px);
  }

  .error {
    padding: var(--page-pad);
    font-size: var(--font-md);
    color: #fca5a5;
  }
</style>
