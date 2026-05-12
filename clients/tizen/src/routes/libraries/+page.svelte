<script lang="ts">
  // Libraries index — top-level grid of every library the user has
  // access to. Each tile drills into /library/[id] for the full item
  // listing. Mirrors the Android TV LibrariesFragment shape: name +
  // type chip, no thumbnails for v1 (text-only tiles are readable at
  // TV distance and avoid an N-query per-library first-item fetch).

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type Library } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { pushTo } from '$lib/nav';

  let libraries = $state<Library[] | null>(null);
  let error = $state('');

  onMount(() => {
    (async () => {
      try {
        libraries = await endpoints.libraries.list();
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    return focusManager.pushBack(() => {
      goto('#/hub');
      return true;
    });
  });

  // Loose categorization for the type chip — "movie" → "Movies",
  // "show" → "TV Shows", etc. Server returns the raw scan type;
  // we humanize it here.
  function typeLabel(t: string): string {
    switch (t) {
      case 'movie': return 'Movies';
      case 'show': return 'TV Shows';
      case 'music': return 'Music';
      case 'photo': return 'Photos';
      case 'audiobook': return 'Audiobooks';
      case 'podcast': return 'Podcasts';
      case 'book': return 'Books';
      case 'home_video': return 'Home Videos';
      default: return t;
    }
  }
</script>

<div class="page">
  <TopNav />
  <h1>Libraries</h1>

  {#if error}
    <p class="error">{error}</p>
  {:else if !libraries}
    <Spinner />
  {:else if libraries.length === 0}
    <p class="empty">No libraries yet. Add one from the web settings UI.</p>
  {:else}
    <div class="grid">
      {#each libraries as lib, i (lib.id)}
        <button
          use:focusable={{ autofocus: i === 0 }}
          class="lib-card"
          onclick={() => pushTo(`#/library/${lib.id}`)}
        >
          <span class="lib-name">{lib.name}</span>
          <span class="lib-type">{typeLabel(lib.type)}</span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: 0 var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .empty, .error {
    font-size: var(--font-md);
  }
  .empty { color: var(--text-secondary); }
  .error { color: #fca5a5; }

  .grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 24px;
  }

  .lib-card {
    font-family: inherit;
    background: var(--bg-elevated);
    border: 2px solid var(--border);
    border-radius: 16px;
    padding: 36px 40px;
    color: var(--text-primary);
    text-align: left;
    display: flex;
    flex-direction: column;
    gap: 10px;
    cursor: pointer;
    min-height: 140px;
  }

  .lib-name {
    font-size: var(--font-lg);
    font-weight: 600;
    line-height: 1.2;
  }

  .lib-type {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.15em;
  }
</style>
