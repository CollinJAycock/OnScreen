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

  let item = $state<ItemDetail | null>(null);
  let children = $state<ChildItem[]>([]);
  let error = $state('');

  const itemId = $derived(page.params.id!);
  const origin = api.getOrigin() ?? '';
  const fanartUrl = $derived(
    item?.fanart_path ? `${origin}/artwork/${item.fanart_path}?w=1920` : ''
  );

  onMount(() => {
    (async () => {
      try {
        item = await endpoints.items.get(itemId);
        if (item.type === 'show' || item.type === 'season') {
          children = await endpoints.items.children(itemId);
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
        else error = (e as Error).message;
      }
    })();

    return focusManager.pushBack(() => {
      history.back();
      return true;
    });
  });

  function play() {
    goto(`/watch/${itemId}`);
  }

  function playChild(childId: string) {
    goto(`/watch/${childId}`);
  }

  function openChild(childId: string) {
    goto(`/item/${childId}`);
  }

  function resumeLabel(): string {
    if (!item?.view_offset_ms) return 'Play';
    const mins = Math.floor(item.view_offset_ms / 60000);
    return `Resume · ${mins}m`;
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
      <h1>{item.title}</h1>
      <div class="meta">
        {#if item.year}<span>{item.year}</span>{/if}
        {#if item.content_rating}<span class="pill">{item.content_rating}</span>{/if}
        {#if item.rating}<span>★ {item.rating.toFixed(1)}</span>{/if}
        {#if item.duration_ms}<span>{Math.round(item.duration_ms / 60000)}m</span>{/if}
      </div>
      {#if item.summary}<p class="summary">{item.summary}</p>{/if}

      <div class="actions">
        {#if item.files.length > 0}
          <button use:focusable={{ autofocus: true }} class="btn primary" onclick={play}>
            {resumeLabel()}
          </button>
        {/if}
      </div>

      {#if children.length > 0}
        <section class="children">
          <h2>Episodes</h2>
          <div class="grid">
            {#each children as child (child.id)}
              {#if child.type === 'episode'}
                <PosterCard
                  title={child.index ? `${child.index}. ${child.title}` : child.title}
                  posterPath={child.thumb_path ?? child.poster_path}
                  subtitle={child.duration_ms ? `${Math.round(child.duration_ms / 60000)}m` : undefined}
                  onclick={() => playChild(child.id)}
                />
              {:else}
                <PosterCard
                  title={child.title}
                  posterPath={child.poster_path}
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
    margin-bottom: 60px;
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
