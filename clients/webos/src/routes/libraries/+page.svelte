<script lang="ts">
  // Libraries index — top-level grid of every library the user has
  // access to. Each tile drills into /library/[id] for the full item
  // listing.
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type Library } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf, restoreGuard, restoreKeyed, takeFocusMemo } from '$lib/focus/memory';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { pushTo } from '$lib/nav';

  let libraries = $state<Library[] | null>(null);
  let error = $state('');
  // Back from a library: the first tile holds its autofocus and the one the
  // user opened takes focus again (lib/focus/memory), as on Favorites.
  let restoring = $state(false);

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    const guard = restoreGuard();
    (async () => {
      try {
        libraries = await endpoints.libraries.list();
        if (restoring) {
          await tick();
          if (!restoreKeyed(memo, guard)) focusFirstOf('.grid [data-focusable]', guard);
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    const offBack = focusManager.pushBack(() => {
      goto('#/hub');
      return true;
    });
    return () => {
      guard.end();
      offBack();
    };
  });

  function typeLabel(t: string): string {
    switch (t) {
      case 'movie': return 'Movies';
      case 'show': return 'TV Shows';
      case 'anime': return 'Anime';
      case 'cartoons': return 'Cartoons';
      case 'dvr': return 'DVR Recordings';
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
          use:focusable={{ autofocus: i === 0 && !restoring }}
          data-focus-key={`library:${lib.id}`}
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
    cursor: pointer;
    min-height: 140px;
  }

  .lib-name {
    font-size: var(--font-lg);
    font-weight: 600;
    line-height: 1.2;
  }

  .lib-type {
    /* Not flexbox `gap` (Chrome 84; webOS 6 runs Chromium 79). */
    margin-top: 10px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.15em;
  }
</style>
