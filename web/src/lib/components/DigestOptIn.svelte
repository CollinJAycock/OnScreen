<script lang="ts">
  // "Email me a digest": the signed-in user's own digest frequency
  // (GET/PUT /users/me/digest). Says plainly when the account has no email
  // address or the server isn't sending digests, so a choice that won't
  // send anything isn't a silent no-op.
  import { onMount } from 'svelte';
  import { digestApi, type DigestFrequency, type MyDigest } from '$lib/userStats';
  import { toast } from '$lib/stores/toast';

  let state: MyDigest | null = null;
  let loading = true;
  let saving = false;
  let error = '';

  const OPTIONS: { value: DigestFrequency; label: string }[] = [
    { value: 'off', label: 'Off' },
    { value: 'weekly', label: 'Weekly' },
    { value: 'monthly', label: 'Monthly' },
  ];

  onMount(async () => {
    try {
      state = await digestApi.getMine();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Couldn’t load your digest setting';
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
      toast.success(f === 'off' ? 'Digest turned off' : `You’ll get a ${f} digest`);
    } catch (e: unknown) {
      state = { ...state, frequency: prev };
      toast.error(e instanceof Error ? e.message : 'Couldn’t save');
    } finally {
      saving = false;
    }
  }

  function fmtSent(iso: string): string {
    return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else if state}
  <div class="seg" role="radiogroup" aria-label="Digest email frequency">
    {#each OPTIONS as o}
      <button
        type="button"
        role="radio"
        aria-checked={state.frequency === o.value}
        class:active={state.frequency === o.value}
        disabled={saving}
        on:click={() => choose(o.value)}
      >{o.label}</button>
    {/each}
  </div>
  {#if state.frequency !== 'off' && !state.has_email}
    <p class="note warn">Your account has no email address, so nothing can be sent. Ask an admin to add one.</p>
  {:else if state.frequency !== 'off' && !state.available}
    <p class="note warn">This server isn’t sending digests right now. Your choice is saved and takes effect once an admin turns them on.</p>
  {:else if state.last_sent_at}
    <p class="note">Last sent {fmtSent(state.last_sent_at)}.</p>
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
