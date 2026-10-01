<script lang="ts">
  // "Request more seasons" on a show page: shown when the show has aired
  // seasons the library doesn't (fully) hold that the user hasn't requested
  // yet, and the user may request. Opens the season picker; the request goes
  // through POST /requests with the picked seasons like any other.
  import { get } from 'svelte/store';
  import { discoverApi, requestsApi, type SeasonInfo } from '$lib/api';
  import { capabilities, ensureCapabilities } from '$lib/stores/capabilities';
  import { toast } from '$lib/stores/toast';
  import { hasRequestableMissing } from '$lib/seasonPicker';
  import { createdRequestToast } from '$lib/requestToast';
  import { REQUESTS_DISABLED_MESSAGE, isRequestsDisabledError } from '../../routes/search/quota';
  import SeasonPicker from './SeasonPicker.svelte';

  interface Props {
    tmdbId: number;
    title: string;
  }

  let { tmdbId, title }: Props = $props();

  let seasons = $state<SeasonInfo[] | null>(null);
  let canRequest = $state(true);
  let open = $state(false);
  // Ignores a slow response for a show the page has since navigated away from.
  let loadSeq = 0;

  let visible = $derived(canRequest && seasons !== null && hasRequestableMissing(seasons));

  async function loadSeasons(id: number) {
    const seq = ++loadSeq;
    try {
      const list = (await discoverApi.seasons(id)) ?? [];
      if (seq === loadSeq) seasons = list;
    } catch {
      // No TMDB, or TMDB unreachable: no button rather than a broken one.
      if (seq === loadSeq) seasons = null;
    }
  }

  async function loadQuota(): Promise<boolean> {
    try {
      const q = await requestsApi.quota();
      return q?.can_request !== false;
    } catch {
      return true; // older server: let the server decide
    }
  }

  // The season list costs a TMDB lookup, so it's only read for a user who
  // may request, on a server with requests on (no TMDB key: no button, and
  // no call that can only fail).
  async function load(id: number) {
    await ensureCapabilities();
    if (get(capabilities)?.features?.requests === false) {
      canRequest = false;
      return;
    }
    canRequest = await loadQuota();
    if (canRequest) await loadSeasons(id);
  }

  $effect(() => {
    const id = tmdbId;
    seasons = null;
    open = false;
    void load(id);
  });

  async function submit(picked: number[] | undefined): Promise<string | null> {
    try {
      const created = await requestsApi.create({
        type: 'show',
        tmdb_id: tmdbId,
        ...(picked ? { seasons: picked } : {}),
      });
      const t = createdRequestToast(created, title);
      if (t.kind === 'info') toast.info(t.text);
      else toast.success(t.text);
      void loadSeasons(tmdbId);
      return null;
    } catch (e) {
      if (isRequestsDisabledError(e)) {
        canRequest = false;
        return REQUESTS_DISABLED_MESSAGE;
      }
      return e instanceof Error && e.message ? e.message : 'Request failed';
    }
  }
</script>

{#if visible}
  <button type="button" class="rms-trigger" aria-haspopup="dialog" onclick={() => (open = true)}>
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14" aria-hidden="true">
      <path d="M12 5v14M5 12h14" />
    </svg>
    <span>Request more seasons</span>
  </button>
{/if}
{#if open}
  <SeasonPicker {tmdbId} {title} onsubmit={submit} onclose={() => (open = false)} />
{/if}

<style>
  .rms-trigger {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    margin-left: 0.5rem;
    vertical-align: middle;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted);
    font-size: 0.75rem;
    font-weight: 500;
    padding: 0.35rem 0.7rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .rms-trigger:hover {
    color: var(--text-secondary);
    background: var(--bg-hover);
  }
</style>
