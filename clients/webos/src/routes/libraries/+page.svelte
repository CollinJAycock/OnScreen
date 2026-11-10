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
  import { i18n, t } from '$lib/i18n/index.svelte';
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

  // In the app's language (read in the template, so it follows a change);
  // a type this build doesn't know shows as the server sends it.
  function typeLabel(type: string): string {
    switch (type) {
      case 'movie': return t('libraries.typeMovie');
      case 'show': return t('libraries.typeShow');
      case 'anime': return t('libraries.typeAnime');
      case 'cartoons': return t('libraries.typeCartoons');
      case 'dvr': return t('libraries.typeDvr');
      case 'music': return t('libraries.typeMusic');
      case 'photo': return t('libraries.typePhoto');
      case 'audiobook': return t('libraries.typeAudiobook');
      case 'podcast': return t('libraries.typePodcast');
      case 'book': return t('libraries.typeBook');
      case 'home_video': return t('libraries.typeHomeVideo');
      default: return type;
    }
  }
</script>

<div class="page">
  <TopNav />
  <h1>{$i18n('nav.libraries')}</h1>

  {#if error}
    <p class="error">{error}</p>
  {:else if !libraries}
    <Spinner />
  {:else if libraries.length === 0}
    <p class="empty">{$i18n('libraries.empty')}</p>
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
