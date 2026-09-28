<script lang="ts">
  // "Play on…" — the cross-device transfer sender.
  //
  // Lists the other OnScreen clients this user has played on recently
  // (GET /playback/devices, last 30 days) and hands the current item +
  // playhead to the chosen one (POST /playback/transfer). The receiving
  // client — TV apps, phones, another browser — filters the resulting SSE
  // event by its own client name and starts playback. See $lib/play-on
  // for the server contract.
  import { onDestroy } from 'svelte';
  import { itemApi, getClientName } from '$lib/api';
  import { formatLastSeen, otherDevices, transferPositionMs, type PlaybackDevice } from '$lib/play-on';

  interface Props {
    itemId: string;
    /** Playhead to hand over, in content milliseconds. Omitted = 0. */
    getPositionMs?: () => number;
    /** Fired after the server accepted the transfer (e.g. pause locally). */
    onsent?: (deviceName: string) => void;
    /** 'overlay' = icon button for the dark player controls (menu opens
     *  upward); 'detail' = labelled pill for an item page's action row. */
    variant?: 'overlay' | 'detail';
  }

  let { itemId, getPositionMs, onsent, variant = 'detail' }: Props = $props();

  let open = $state(false);
  let loading = $state(false);
  let devices = $state<PlaybackDevice[]>([]);
  let loadError = $state('');
  let sendError = $state('');
  let sendingTo = $state('');
  let sentTo = $state('');
  let now = $state(Date.now());
  let root = $state<HTMLDivElement>();
  // Guards against a slow device list landing after the menu was closed
  // and reopened.
  let loadSeq = 0;
  let sentTimer: ReturnType<typeof setTimeout> | null = null;

  function errText(e: unknown): string {
    return e instanceof Error && e.message ? e.message : 'request failed';
  }

  async function loadDevices() {
    const seq = ++loadSeq;
    loading = true;
    loadError = '';
    try {
      const list = await itemApi.listDevices();
      if (seq !== loadSeq) return;
      devices = otherDevices(list, getClientName());
      now = Date.now();
    } catch (e) {
      if (seq !== loadSeq) return;
      devices = [];
      loadError = `Couldn't load devices: ${errText(e)}`;
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  function close() {
    open = false;
    sendError = '';
    loadSeq++;
    loading = false;
  }

  function toggle() {
    if (open) {
      close();
      return;
    }
    open = true;
    sendError = '';
    sentTo = '';
    void loadDevices();
  }

  async function send(name: string) {
    if (sendingTo) return;
    sendingTo = name;
    sendError = '';
    const positionMs = transferPositionMs(getPositionMs);
    try {
      await itemApi.transfer(itemId, name, positionMs);
      open = false;
      sentTo = name;
      if (sentTimer) clearTimeout(sentTimer);
      sentTimer = setTimeout(() => {
        sentTo = '';
      }, 5000);
      onsent?.(name);
    } catch (e) {
      sendError = `Couldn't send to ${name}: ${errText(e)}`;
    } finally {
      sendingTo = '';
    }
  }

  function onWindowClick(e: MouseEvent) {
    if (open && root && !root.contains(e.target as Node)) close();
  }

  function onWindowKeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape') close();
  }

  onDestroy(() => {
    if (sentTimer) clearTimeout(sentTimer);
  });
</script>

<svelte:window onclick={onWindowClick} onkeydown={onWindowKeydown} />

<!-- Clicks inside must not reach the host: the player's controls overlay
     toggles play/pause on click. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="play-on {variant}" bind:this={root} onclick={(e) => e.stopPropagation()}>
  <button
    type="button"
    class="trigger"
    class:open
    aria-haspopup="menu"
    aria-expanded={open}
    title="Play on another device"
    aria-label="Play on another device"
    onclick={toggle}
  >
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16" aria-hidden="true">
      <rect x="2" y="4" width="20" height="13" rx="2" />
      <path d="M8 21h8M12 17v4" />
      <path d="M10 8.2v5.6l4.6-2.8z" fill="currentColor" stroke="none" />
    </svg>
    {#if variant === 'detail'}<span>Play on…</span>{/if}
  </button>

  {#if open}
    <div class="menu" role="menu" aria-label="Play on">
      <div class="menu-head">Play on</div>
      {#if loading}
        <div class="state">Looking for devices…</div>
      {:else if loadError}
        <div class="state error" role="alert">
          {loadError}
          <button type="button" class="retry" onclick={loadDevices}>Retry</button>
        </div>
      {:else if devices.length === 0}
        <div class="state">No other devices seen recently — open OnScreen on your TV or phone first.</div>
      {:else}
        {#each devices as d (d.client_name)}
          <button
            type="button"
            role="menuitem"
            class="device"
            disabled={sendingTo !== ''}
            onclick={() => send(d.client_name)}
          >
            <span class="device-name">{d.client_name}</span>
            <span class="device-seen">
              {#if sendingTo === d.client_name}
                Sending…
              {:else if formatLastSeen(d.last_seen, now)}
                Last seen {formatLastSeen(d.last_seen, now)}
              {/if}
            </span>
          </button>
        {/each}
      {/if}
      {#if sendError}
        <div class="state error" role="alert">{sendError}</div>
      {/if}
    </div>
  {/if}

  {#if sentTo}
    <div class="sent" role="status">Sent to {sentTo}</div>
  {/if}
</div>

<style>
  .play-on {
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .play-on.detail {
    margin-left: 0.5rem;
    vertical-align: middle;
  }

  .trigger {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  /* Player controls: same look as the page's .icon-btn.small. */
  .overlay .trigger {
    background: none;
    border: none;
    color: rgba(255, 255, 255, 0.9);
    padding: 0.3rem;
    border-radius: 6px;
  }
  .overlay .trigger:hover,
  .overlay .trigger.open {
    background: rgba(255, 255, 255, 0.1);
    color: #fff;
  }
  /* Item page: matches the Download pill next to it. */
  .detail .trigger {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted);
    font-size: 0.75rem;
    font-weight: 500;
    padding: 0.35rem 0.7rem;
  }
  .detail .trigger:hover,
  .detail .trigger.open {
    color: var(--text-secondary);
    background: var(--bg-hover);
  }

  .menu {
    position: absolute;
    z-index: 40;
    min-width: 230px;
    max-width: 320px;
    padding: 0.35rem;
    border-radius: 8px;
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
  }
  .overlay .menu {
    bottom: calc(100% + 8px);
    right: 0;
    background: rgba(15, 15, 25, 0.95);
    border: 1px solid rgba(255, 255, 255, 0.12);
    backdrop-filter: blur(12px);
    box-shadow: 0 4px 24px rgba(0, 0, 0, 0.5);
    color: rgba(255, 255, 255, 0.85);
  }
  .detail .menu {
    top: calc(100% + 6px);
    left: 0;
    background: var(--bg-elevated, var(--bg-secondary));
    border: 1px solid var(--border);
    box-shadow: 0 6px 24px rgba(0, 0, 0, 0.3);
    color: var(--text-secondary);
  }

  .menu-head {
    font-size: 0.68rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    opacity: 0.6;
    padding: 0.3rem 0.6rem 0.2rem;
  }

  .device {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.1rem;
    background: none;
    border: none;
    border-radius: 5px;
    color: inherit;
    cursor: pointer;
    padding: 0.45rem 0.6rem;
    text-align: left;
    transition: background 0.1s;
  }
  .overlay .device:hover:not(:disabled) { background: rgba(255, 255, 255, 0.1); color: #fff; }
  .detail .device:hover:not(:disabled) { background: var(--bg-hover); color: var(--text-primary); }
  .device:disabled { cursor: progress; opacity: 0.7; }
  .device-name { font-size: 0.82rem; font-weight: 500; }
  .device-seen { font-size: 0.68rem; opacity: 0.6; }

  .state {
    font-size: 0.75rem;
    line-height: 1.4;
    padding: 0.4rem 0.6rem;
    opacity: 0.85;
  }
  .state.error { color: #f87171; opacity: 1; }
  .retry {
    background: none;
    border: none;
    color: inherit;
    text-decoration: underline;
    cursor: pointer;
    font-size: inherit;
    padding: 0 0 0 0.25rem;
  }

  .sent {
    position: absolute;
    white-space: nowrap;
    font-size: 0.72rem;
    padding: 0.25rem 0.55rem;
    border-radius: 5px;
    pointer-events: none;
  }
  .overlay .sent {
    bottom: calc(100% + 8px);
    right: 0;
    background: rgba(15, 15, 25, 0.9);
    color: #fff;
  }
  .detail .sent {
    top: calc(100% + 6px);
    left: 0;
    background: var(--bg-elevated, var(--bg-secondary));
    border: 1px solid var(--border);
    color: var(--text-secondary);
  }
</style>
