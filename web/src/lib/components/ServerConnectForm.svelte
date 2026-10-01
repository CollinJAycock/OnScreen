<script lang="ts">
  // Desktop client: the "server address" field used by the first-run screen,
  // the can't-reach-the-server screen and /native/server. It takes what
  // people actually type — "10.0.0.66:7070", "nas.local:7070",
  // "https://media.example.com" — tests it (see $lib/serverConnect) and hands
  // the working URL to `onConnected`, which saves it and reloads the app.
  //
  // A plain text field, not type="url": the browser's URL validation blocked
  // every bare host with a terse "Please enter a URL." before this code ran.
  import { tick } from 'svelte';
  import { connectToServer, errorText, type ConnectResult } from '$lib/serverConnect';

  interface Props {
    /** Pre-filled address (the current server when changing it). */
    initial?: string;
    submitLabel?: string;
    /** Saves the result and reloads the app. Throwing shows its message
     *  under the field. */
    onConnected: (result: ConnectResult) => Promise<void> | void;
    autofocus?: boolean;
  }

  let {
    initial = '',
    submitLabel = 'Connect',
    onConnected,
    autofocus = false,
  }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);
  // svelte-ignore state_referenced_locally -- seeds the field once; later edits are the user's
  let value = $state(initial);
  let error = $state('');
  let status = $state('');
  let busy = $state(false);
  let inputEl = $state<HTMLInputElement>();

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    error = '';
    if (!value.trim()) {
      error = "Enter your OnScreen server's address.";
      return;
    }
    busy = true;
    status = 'Testing the connection…';
    try {
      const result = await connectToServer(value, { onStatus: (s) => (status = s) });
      status = 'Connected — saving…';
      await onConnected(result);
      // onConnected reloads the app; stay busy until it does.
    } catch (err) {
      error = errorText(err);
      status = '';
      busy = false;
      // Back to the field, ready for a corrected address.
      await tick();
      inputEl?.focus();
    }
  }
</script>

<form class="server-connect" onsubmit={submit} novalidate>
  <label for="server-address-{uid}">Server address</label>
  <!-- svelte-ignore a11y_autofocus -- the only field on a setup screen -->
  <input
    id="server-address-{uid}"
    type="text"
    inputmode="url"
    autocapitalize="off"
    autocomplete="off"
    spellcheck="false"
    placeholder="192.168.1.50:7070 or https://onscreen.example.com"
    bind:value
    bind:this={inputEl}
    oninput={() => (error = '')}
    readonly={busy}
    aria-busy={busy}
    {autofocus}
    aria-invalid={error ? 'true' : undefined}
    aria-describedby={error ? `server-address-error-${uid}` : undefined}
  />
  {#if error}
    <div class="error" id="server-address-error-{uid}" role="alert">{error}</div>
  {:else if busy && status}
    <div class="status" role="status">{status}</div>
  {/if}
  <button type="submit" disabled={busy}>
    {busy ? 'Connecting…' : submitLabel}
  </button>
</form>

<style>
  .server-connect { display: flex; flex-direction: column; gap: 0.6rem; }
  label {
    font-size: 0.78rem; font-weight: 600; color: var(--text-secondary);
  }
  input {
    padding: 0.6rem 0.85rem; background: var(--bg-hover);
    border: 1px solid var(--border-strong); border-radius: 8px;
    color: var(--text-primary); font-size: 0.92rem;
  }
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  input[readonly] { opacity: 0.7; }
  .error {
    background: var(--error-bg); color: var(--error);
    border: 1px solid var(--error-bg);
    padding: 0.55rem 0.8rem; border-radius: 7px; font-size: 0.8rem; line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .status { font-size: 0.8rem; color: var(--text-muted); overflow-wrap: anywhere; }
  button {
    align-self: stretch;
    padding: 0.6rem 1rem; background: var(--accent); border: none;
    border-radius: 8px; color: #fff; font-size: 0.9rem; font-weight: 600; cursor: pointer;
    transition: background 0.15s;
  }
  button:hover { background: var(--accent-hover); }
  button:disabled { opacity: 0.55; cursor: not-allowed; }
</style>
