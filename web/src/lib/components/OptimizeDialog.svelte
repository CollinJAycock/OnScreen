<script lang="ts">
  // "Optimize…" (admin): pick a profile and queue optimized versions — a
  // pre-made MP4 (H.264 + AAC) the player direct-plays instead of a live
  // transcode — for an item, a library, a collection or a selection.
  import { optimizeApi, type OptimizeProfile, type OptimizeProfileId, type OptimizeResult, type OptimizeSelection } from '$lib/api';
  import { optimizeErrorMessage, optimizeResultMessage } from '$lib/optimize';
  import { toast } from '$lib/stores/toast';

  interface Props {
    open: boolean;
    selection: OptimizeSelection;
    /** What is being optimized, for the heading ("Inception", "Movies"). */
    what: string;
    onclose?: () => void;
    ondone?: (r: OptimizeResult) => void;
  }

  let { open, selection, what, onclose, ondone }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let profiles = $state<OptimizeProfile[]>([]);
  let chosen = $state<OptimizeProfileId>('mobile_720p');
  let busy = $state(false);
  let error = $state('');

  $effect(() => {
    if (open && profiles.length === 0) void load();
  });

  async function load() {
    try {
      profiles = (await optimizeApi.profiles()) ?? [];
      if (profiles.length && !profiles.some((p) => p.id === chosen)) chosen = profiles[0].id;
    } catch (e) {
      error = optimizeErrorMessage(e);
    }
  }

  async function submit() {
    busy = true;
    error = '';
    try {
      const r = await optimizeApi.create(selection, chosen);
      toast.success(optimizeResultMessage(r));
      ondone?.(r);
      onclose?.();
    } catch (e) {
      error = optimizeErrorMessage(e);
    } finally {
      busy = false;
    }
  }

  function describe(p: OptimizeProfile): string {
    if (p.keep_resolution) return 'Same resolution, re-encoded so every device can play it. Largest files.';
    return `Up to ${p.max_height}p at about ${p.bitrate_kbps / 1000} Mbps — for phones, weak devices and viewers away from home.`;
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="backdrop" onclick={() => onclose?.()}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="panel" role="dialog" aria-labelledby="opt-title-{uid}" tabindex="-1" onclick={(e) => e.stopPropagation()}>
      <div class="panel-header">
        <span id="opt-title-{uid}">Optimize {what}</span>
        <button class="close-btn" aria-label="Close" onclick={() => onclose?.()}>×</button>
      </div>
      {#if error}<div class="msg error" role="alert">{error}</div>{/if}
      <div class="body">
        <p class="hint">
          The server encodes a copy of each video ahead of time. Players that would otherwise
          need a live transcode — a codec they can't decode, or a bitrate limit away from home —
          play the copy directly. Copies use disk space and server time; they stay until you
          delete them or the original changes.
        </p>
        <fieldset class="profiles">
          <legend class="sr-only">Profile</legend>
          {#each profiles as p (p.id)}
            <label class="profile" class:on={chosen === p.id}>
              <input type="radio" name="opt-profile-{uid}" value={p.id} bind:group={chosen} disabled={busy} />
              <span class="profile-text">
                <span class="profile-name">{p.label}</span>
                <span class="profile-desc">{describe(p)}</span>
              </span>
            </label>
          {:else}
            <p class="hint">Loading profiles…</p>
          {/each}
        </fieldset>
        <div class="actions">
          <button class="btn-x" disabled={busy} onclick={() => onclose?.()}>Cancel</button>
          <button class="btn-go" disabled={busy || profiles.length === 0} onclick={submit}>
            {busy ? 'Queueing…' : 'Optimize'}
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center;
    z-index: 2000;
  }
  .panel {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 12px; width: 420px; max-width: calc(100vw - 2rem);
    box-shadow: 0 20px 60px var(--shadow);
  }
  .panel-header {
    display: flex; align-items: center; justify-content: space-between; gap: 0.5rem;
    padding: 0.8rem 1rem; border-bottom: 1px solid var(--border);
    font-size: 0.85rem; font-weight: 600; color: var(--text-primary);
  }
  .panel-header span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .close-btn {
    background: none; border: none; color: var(--text-muted); font-size: 1.1rem;
    cursor: pointer; padding: 0 0.2rem; line-height: 1;
  }
  .msg { padding: 0.5rem 1rem; font-size: 0.75rem; }
  .msg.error { color: var(--error, #f87171); background: rgba(248,113,113,0.08); }
  .body { padding: 0.8rem 1rem 1rem; }
  .hint { font-size: 0.74rem; color: var(--text-muted); margin: 0 0 0.75rem; line-height: 1.45; }
  .profiles { border: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .profile {
    display: flex; align-items: flex-start; gap: 0.6rem;
    padding: 0.55rem 0.7rem; border-radius: 8px;
    border: 1px solid var(--border-strong); background: var(--input-bg);
    cursor: pointer;
  }
  .profile.on { border-color: var(--accent); background: var(--accent-bg); }
  /* Not caught by form-wide width rules: a radio stays its own size. */
  .profile input { width: auto; margin: 0.15rem 0 0; flex-shrink: 0; accent-color: var(--accent); }
  .profile-text { display: flex; flex-direction: column; gap: 0.15rem; min-width: 0; }
  .profile-name { font-size: 0.8rem; font-weight: 600; color: var(--text-primary); }
  .profile-desc { font-size: 0.72rem; color: var(--text-secondary); line-height: 1.35; }
  .actions { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.9rem; }
  .btn-go {
    padding: 0.4rem 0.9rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-x {
    padding: 0.4rem 0.9rem; background: var(--input-bg); border: 1px solid var(--border);
    border-radius: 6px; color: var(--text-secondary); font-size: 0.78rem; cursor: pointer;
  }
  .btn-go:disabled, .btn-x:disabled { opacity: 0.5; cursor: not-allowed; }
  .sr-only {
    position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
    overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0;
  }
</style>
