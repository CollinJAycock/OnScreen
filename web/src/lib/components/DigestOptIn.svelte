<script lang="ts">
  // "Email me a digest": the signed-in user's own digest frequency
  // (GET/PUT /users/me/digest). Says plainly when the account has no email
  // address or the server isn't sending digests, so a choice that won't
  // send anything isn't a silent no-op.
  import { onMount } from 'svelte';
  import { digestApi, type DigestFrequency, type MyDigest } from '$lib/userStats';
  import { toast } from '$lib/stores/toast';
  import { t, i18n, type MessageKey } from '$lib/i18n/index.svelte';

  let state: MyDigest | null = null;
  let loading = true;
  let saving = false;
  let error = '';

  const OPTIONS: { value: DigestFrequency; label: MessageKey }[] = [
    { value: 'off', label: 'player.off' },
    { value: 'weekly', label: 'ui.digest.weekly' },
    { value: 'monthly', label: 'ui.digest.monthly' },
  ];

  onMount(async () => {
    try {
      state = await digestApi.getMine();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('ui.digest.mineLoadFailed');
    } finally {
      loading = false;
    }
  });

  async function choose(f: DigestFrequency) {
    if (!state || f === state.frequency || saving) return;
    const prev = state.frequency;
    state = { ...state, frequency: f };
    saving = true;
    try {
      state = await digestApi.setMine(f);
      toast.success(f === 'off' ? t('ui.digest.turnedOff') : f === 'weekly' ? t('ui.digest.youGetWeekly') : t('ui.digest.youGetMonthly'));
    } catch (e: unknown) {
      state = { ...state, frequency: prev };
      toast.error(e instanceof Error ? e.message : t('ui.metadata.saveFailed'));
    } finally {
      saving = false;
    }
  }

  function fmtSent(iso: string): string {
    return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
  }
</script>

{#if loading}
  <p class="muted">{$i18n('player.loading')}</p>
{:else if error}
  <p class="error">{error}</p>
{:else if state}
  <div class="seg" role="radiogroup" aria-label={$i18n('ui.digest.frequency')}>
    {#each OPTIONS as o}
      <button
        type="button"
        role="radio"
        aria-checked={state.frequency === o.value}
        class:active={state.frequency === o.value}
        disabled={saving}
        on:click={() => choose(o.value)}
      >{$i18n(o.label)}</button>
    {/each}
  </div>
  {#if state.frequency !== 'off' && !state.has_email}
    <p class="note warn">{$i18n('ui.digest.noEmail')}</p>
  {:else if state.frequency !== 'off' && !state.available}
    <p class="note warn">{$i18n('ui.digest.unavailable')}</p>
  {:else if state.last_sent_at}
    <p class="note">{$i18n('ui.digest.lastSent', { date: fmtSent(state.last_sent_at) })}</p>
  {/if}
{/if}

<style>
  .seg { display: inline-flex; border: 1px solid var(--border-strong); border-radius: 8px; overflow: hidden; }
  .seg button {
    width: auto; padding: 0.42rem 1rem; font-size: 0.8rem; font-family: inherit; cursor: pointer;
    border: 0; background: transparent; color: var(--text-secondary);
  }
  .seg button + button { border-left: 1px solid var(--border-strong); }
  .seg button:hover:not(:disabled) { background: var(--bg-hover); }
  .seg button.active { background: var(--accent-bg); color: var(--accent-text); font-weight: 600; }
  .seg button:disabled { cursor: progress; }
  .note { margin: 0.7rem 0 0; font-size: 0.8rem; color: var(--text-muted); line-height: 1.45; }
  .note.warn { color: var(--text-secondary); }
  .muted { color: var(--text-muted); font-size: 0.82rem; margin: 0; }
  .error { color: var(--error); font-size: 0.82rem; margin: 0; }
</style>
