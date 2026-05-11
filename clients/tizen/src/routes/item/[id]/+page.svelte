<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    api,
    endpoints,
    Unauthorized,
    type ItemDetail,
    type ChildItem
  } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import Spinner from '$lib/components/Spinner.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import { goBack, openChild as openChildNav } from '$lib/nav';

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

  const itemId = $derived(page.params.id!);
  const fanartUrl = $derived(
    item?.fanart_path ? api.assetUrl(`/artwork/${item.fanart_path}?w=1920`) : ''
  );
  const posterUrl = $derived(
    item?.poster_path ? api.assetUrl(`/artwork/${item.poster_path}?w=480`) : ''
  );

  // book_author + book_series have no playable file of their own and
  // we deliberately hide the "Play first child" button because the
  // first child is itself a parent (a series under an author, a book
  // under a series). With no autofocus target, the remote can't drive
  // the page — so when the Play button is suppressed, autofocus the
  // first grid card instead.
  const autofocusGridFirstCard = $derived(
    !!item && item.files.length === 0 &&
      (item.type === 'book_author' || item.type === 'book_series')
  );

  onMount(() => {
    (async () => {
      try {
        item = await endpoints.items.get(itemId);
        // Container types load children. "audiobook" is dual-shape:
        // single-file books have files of their own (children empty),
        // multi-file books expose audiobook_chapter children.
        // book_author + book_series are the audiobook hierarchy
        // parents above an audiobook row — drilling into either
        // renders the children list (series + standalone books for
        // an author, books for a series).
        if (
          item.type === 'show' ||
          item.type === 'season' ||
          item.type === 'album' ||
          item.type === 'podcast' ||
          item.type === 'audiobook' ||
          item.type === 'book_author' ||
          item.type === 'book_series'
        ) {
          const raw = await endpoints.items.children(itemId);
          // book_author: series alphabetical first, then standalone
          // books year-desc. Mirrors the Android TV + phone bucket
          // ordering so the same browse mental model holds across
          // surfaces. Other types pass through untouched.
          if (item.type === 'book_author') {
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
            children = [...series, ...books];
          } else {
            children = raw;
          }

        }

        // Show context (show/season/episode): assemble the show's
        // season list + selected season's episodes so the page renders
        // the full hierarchy instead of just one layer.
        if (item.type === 'show') {
          showSeasons = children;
          if (children.length > 0) void selectSeason(children[0].id);
        } else if (item.type === 'season') {
          // children = this season's episodes (already loaded above).
          // Seed seasonEpisodes from it and fetch sibling seasons via
          // the show (parent_id).
          seasonEpisodes = children;
          selectedSeasonId = item.id;
          if (item.parent_id) {
            try {
              showSeasons = await endpoints.items.children(item.parent_id);
            } catch { showSeasons = []; }
          }
        } else if (item.type === 'episode' && item.parent_id) {
          // Walk up to the season → the show. Two extra round-trips
          // for the show id; tolerable since episode pages are a
          // deep-link surface (Continue Watching tile, search hit).
          try {
            const season = await endpoints.items.get(item.parent_id);
            const showId = season.parent_id;
            selectedSeasonId = season.id;
            const [seasons, episodes] = await Promise.all([
              showId ? endpoints.items.children(showId) : Promise.resolve([] as ChildItem[]),
              endpoints.items.children(season.id),
            ]);
            showSeasons = seasons;
            seasonEpisodes = episodes;
          } catch {
            // Best-effort; an episode page with hero + Play still works.
          }
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    return focusManager.pushBack(() => {
      goBack();
      return true;
    });
  });

  function play() {
    goto(`#/watch/${itemId}`);
  }

  function playChild(childId: string) {
    goto(`#/watch/${childId}`);
  }

  function openChild(childId: string) {
    openChildNav(childId);
  }

  async function selectSeason(seasonId: string) {
    if (selectedSeasonId === seasonId) return;
    selectedSeasonId = seasonId;
    seasonEpisodesLoading = true;
    try {
      seasonEpisodes = await endpoints.items.children(seasonId);
    } catch {
      seasonEpisodes = [];
    } finally {
      seasonEpisodesLoading = false;
    }
  }

  function resumeLabel(): string {
    if (!item?.view_offset_ms) return 'Play';
    const mins = Math.floor(item.view_offset_ms / 60000);
    return `Resume · ${mins}m`;
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
            {#if item.rating}<span>★ {item.rating.toFixed(1)}</span>{/if}
            {#if item.duration_ms}<span>{Math.round(item.duration_ms / 60000)}m</span>{/if}
          </div>
          {#if item.summary}<p class="summary">{item.summary}</p>{/if}

          <div class="actions">
        {#if item.type === 'book'}
          <!-- Ebooks (EPUB / CBZ / CBR) need a paginated reader; the
               TV clients don't ship one. Show a clear message instead
               of routing to /watch, which would stall trying to play
               an archive file as video. -->
          <div class="note">Book reading isn't available on TV. Open this book in the web or phone app.</div>
        {:else if item.files.length > 0}
          <button use:focusable={{ autofocus: true }} class="btn primary" onclick={play}>
            {resumeLabel()}
          </button>
        {:else if item.type === 'show' && seasonEpisodes.length > 0}
          <!-- Show pages: Play drills two layers down to a real
               playable episode. children[0] is a SEASON (no file),
               so we use the selected season's first episode instead. -->
          <button use:focusable={{ autofocus: true }} class="btn primary" onclick={() => playChild(seasonEpisodes[0].id)}>
            Play S{seasonEpisodes[0].index ?? 1}E1
          </button>
        {:else if children.length > 0 && item.type !== 'book_author' && item.type !== 'book_series'}
          <!-- Container types (season / album / podcast / multi-
               file audiobook) where children[0] IS playable. Show
               type is handled in the branch above because its
               children are seasons, not episodes.

               book_author + book_series are pure browse parents —
               the first child is itself a parent (series under an
               author, multi-file book under a series), so Play
               would land on a non-playable row. Hide and let the
               user pick a book. -->
          <button use:focusable={{ autofocus: true }} class="btn primary" onclick={() => playChild(children[0].id)}>
            Play
          </button>
        {/if}
          </div>
        </div>
      </div>

      {#if showSeasons.length > 0}
        <!-- Two-tier show layout: season chips along the top, episode
             grid below for the selected season. Rendered for show,
             season, and episode pages alike so the user always sees
             the full hierarchy. -->
        <section class="children">
          <h2>Seasons</h2>
          <div class="season-chips">
            {#each showSeasons as season (season.id)}
              <button
                use:focusable
                class="season-chip"
                class:active={selectedSeasonId === season.id}
                onclick={() => selectSeason(season.id)}
              >
                {season.title}
              </button>
            {/each}
          </div>

          {#if seasonEpisodesLoading}
            <Spinner />
          {:else if seasonEpisodes.length > 0}
            <h2 class="episodes-heading">Episodes</h2>
            <div class="grid">
              {#each seasonEpisodes as ep (ep.id)}
                <PosterCard
                  title={ep.index ? `${ep.index}. ${ep.title}` : ep.title}
                  posterPath={ep.thumb_path ?? ep.poster_path}
                  subtitle={ep.duration_ms ? `${Math.round(ep.duration_ms / 60000)}m` : undefined}
                  onclick={() => playChild(ep.id)}
                />
              {/each}
            </div>
          {/if}
        </section>
      {:else if children.length > 0}
        <section class="children">
          <h2>{childrenHeading(item.type)}</h2>
          <div class="grid">
            {#each children as child, i (child.id)}
              {#if child.type === 'episode' || child.type === 'audiobook_chapter' || child.type === 'track' || child.type === 'podcast_episode'}
                <PosterCard
                  title={child.index ? `${child.index}. ${child.title}` : child.title}
                  posterPath={child.thumb_path ?? child.poster_path}
                  subtitle={child.duration_ms ? `${Math.round(child.duration_ms / 60000)}m` : undefined}
                  autofocus={autofocusGridFirstCard && i === 0}
                  onclick={() => playChild(child.id)}
                />
              {:else}
                <PosterCard
                  title={child.title}
                  posterPath={child.poster_path}
                  autofocus={autofocusGridFirstCard && i === 0}
                  onclick={() => openChild(child.id)}
                />
              {/if}
            {/each}
          </div>
        </section>
      {/if}
    </div>
  </div>
{/if}

<style>
  .page {
    position: relative;
    min-height: 100%;
  }

  .fanart {
    position: absolute;
    inset: 0;
    background-size: cover;
    background-position: center top;
    filter: brightness(0.4);
    z-index: 0;
  }

  .fanart-scrim {
    position: absolute;
    inset: 0;
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
    gap: 48px;
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
  }

  .hero-text {
    flex: 1;
    min-width: 0;
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 0 0 20px;
  }

  .meta {
    display: flex;
    gap: 24px;
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin-bottom: 24px;
  }

  .pill {
    border: 2px solid var(--border-strong);
    padding: 2px 12px;
    border-radius: 6px;
    font-size: var(--font-sm);
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
    gap: 24px;
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

  .children h2 {
    font-size: var(--font-lg);
    margin: 0 0 24px;
  }

  .episodes-heading {
    margin-top: 48px !important;
  }

  .season-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 16px;
    margin-bottom: 32px;
  }

  .season-chip {
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

  .grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: var(--card-gap);
  }

  .error {
    padding: var(--page-pad);
    font-size: var(--font-md);
    color: #fca5a5;
  }
</style>
