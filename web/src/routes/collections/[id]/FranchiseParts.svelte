<script lang="ts">
  // A franchise collection's films: owned ones as normal cards, missing ones
  // greyed with a Request button (or their open request's status). Missing
  // parts only reach the client for profiles without a rating ceiling — the
  // server filters them — so there's no client-side gate to get wrong.
  // The Request button itself only shows to an account that may request on
  // a server with requests on; otherwise missing films are just greyed out.
  import { onMount } from 'svelte';
  import { capabilities } from '$lib/stores/capabilities';
  import {
    requestsApi,
    assetUrl,
    type CollectionItem,
    type FranchisePart,
    type RequestQuota,
  } from '$lib/api';
  import {
    applyRequestResult,
    collectionRows,
    ownedPosterPath,
    ownedSummary,
    partState,
    requestStatusLabel,
  } from '$lib/franchise';
  import { OVER_QUOTA_MESSAGE, REQUESTS_DISABLED_MESSAGE, isRequestsDisabledError } from '../../search/quota';

  interface Props {
    parts: FranchisePart[] | undefined;
    items: CollectionItem[];
  }

  let { parts, items }: Props = $props();

  let rows = $state<FranchisePart[]>([]);
  let requesting = $state<Set<number>>(new Set());
  let message = $state('');
  let error = $state('');
  // null = unknown (older server / lookup failed): buttons stay and the
  // server has the final word. An explicit "no" — from the account's
  // allowance or the server's requests flag — removes them.
  let quota = $state<RequestQuota | null>(null);
  let canRequest = $derived(quota?.can_request !== false && $capabilities?.features?.requests !== false);

  $effect(() => {
    rows = collectionRows(parts, items);
  });

  let ownedCount = $derived(rows.filter((r) => !!r.item_id).length);
  let hasMissing = $derived(rows.some((r) => !r.item_id));

  onMount(async () => {
    if (!(parts ?? []).some((p) => !p.item_id)) return; // nothing to request
    try {
      quota = (await requestsApi.quota()) ?? null;
    } catch {
      quota = null;
    }
  });

  async function request(p: FranchisePart) {
    if (requesting.has(p.tmdb_id)) return;
    requesting = new Set(requesting).add(p.tmdb_id);
    message = '';
    error = '';
    try {
      const created = await requestsApi.create({ type: 'movie', tmdb_id: p.tmdb_id });
      rows = applyRequestResult(rows, p.tmdb_id, created.status);
      if (created.over_quota) {
        message = `${OVER_QUOTA_MESSAGE}: ${p.title}`;
      } else if (created.auto_approved || created.status === 'approved' || created.status === 'downloading') {
        message = `Approved automatically — it's on its way: ${p.title}`;
      } else {
        message = `Requested: ${p.title} — awaiting admin approval`;
      }
    } catch (e: unknown) {
      if (isRequestsDisabledError(e)) {
        error = REQUESTS_DISABLED_MESSAGE;
        quota = quota ? { ...quota, can_request: false } : quota;
      } else {
        // The server's own message (403 quota/permission, 409 already
        // requested, …) says it best.
        error = e instanceof Error && e.message ? e.message : 'Request failed';
      }
    } finally {
      const next = new Set(requesting);
      next.delete(p.tmdb_id);
      requesting = next;
    }
  }
</script>

<p class="summary">{ownedSummary(ownedCount, rows.length)}{#if hasMissing} · missing films are greyed out{/if}</p>

{#if message}<p class="msg" role="status">{message}</p>{/if}
{#if error}<p class="err" role="alert">{error}</p>{/if}

<div class="grid">
  {#each rows as p (p.item_id ?? `tmdb-${p.tmdb_id}`)}
    {@const ps = partState(p)}
    {@const art = ownedPosterPath(p, items)}
    {#if ps === 'owned'}
      <a class="card" href="/watch/{p.item_id}" data-testid="part-owned">
        {#if art}
          <img class="poster" src={assetUrl(`/artwork/${encodeURI(art)}?w=300`)} alt={p.title} loading="lazy" />
        {:else if p.poster_url}
          <img class="poster" src={p.poster_url} alt={p.title} loading="lazy" referrerpolicy="no-referrer" />
        {:else}
          <div class="poster placeholder"><span>🎬</span></div>
        {/if}
        <div class="meta">
          <div class="title">{p.title}</div>
          {#if p.year}<div class="sub">{p.year}</div>{/if}
        </div>
      </a>
    {:else}
      <div class="card missing" data-testid="part-missing" title={p.overview ?? ''}>
        {#if p.poster_url}
          <img class="poster" src={p.poster_url} alt={p.title} loading="lazy" referrerpolicy="no-referrer" />
        {:else}
          <div class="poster placeholder"><span>🎬</span></div>
        {/if}
        <div class="meta">
          <div class="title">{p.title}</div>
          {#if p.year}<div class="sub">{p.year}</div>{/if}
          {#if ps === 'requested'}
            <span class="req-status">{requestStatusLabel(p.request_status)}</span>
          {:else if canRequest}
            <button
              type="button"
              class="req-btn"
              disabled={requesting.has(p.tmdb_id)}
              title={`Ask for ${p.title} to be added`}
              onclick={() => request(p)}
            >
              {requesting.has(p.tmdb_id) ? 'Requesting…' : 'Request'}
            </button>
          {/if}
        </div>
      </div>
    {/if}
  {/each}
</div>

<style>
  .summary { font-size: 0.78rem; color: var(--text-muted); margin: -1rem 0 1.25rem; }
  .msg { font-size: 0.8rem; color: var(--text-secondary); margin-bottom: 1rem; }
  .err {
    background: var(--error-bg); color: var(--error); padding: 0.55rem 0.85rem;
    border-radius: 8px; font-size: 0.8rem; margin-bottom: 1rem;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 1rem;
  }
  .card {
    display: flex; flex-direction: column; text-decoration: none; color: inherit;
    border-radius: 8px; overflow: hidden; position: relative;
    background: var(--input-bg); border: 1px solid var(--border);
    transition: border-color 0.15s, transform 0.15s;
  }
  a.card:hover { border-color: rgba(124,106,247,0.3); transform: translateY(-2px); }
  .poster { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; }
  .poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    background: rgba(255,255,255,0.02); font-size: 2rem;
  }
  .card.missing .poster { filter: grayscale(1); opacity: 0.4; }
  .card.missing .title { color: var(--text-muted); }
  .meta { padding: 0.6rem 0.5rem; display: flex; flex-direction: column; gap: 0.2rem; }
  .title { font-size: 0.78rem; font-weight: 600; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { font-size: 0.68rem; color: var(--text-muted); }
  .req-btn {
    margin-top: 0.35rem; align-self: flex-start;
    padding: 0.28rem 0.7rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.72rem; font-weight: 600; cursor: pointer;
  }
  .req-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .req-status {
    margin-top: 0.35rem; align-self: flex-start; font-size: 0.7rem; font-weight: 600;
    color: var(--accent-text, var(--accent)); background: var(--accent-bg, transparent);
    border-radius: 6px; padding: 0.2rem 0.55rem;
  }
</style>
