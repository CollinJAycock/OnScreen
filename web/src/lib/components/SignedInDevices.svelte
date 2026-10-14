<script lang="ts">
  // Signed-in devices list: one row per sign-in with a "Sign out" button, plus
  // a bulk action ("Sign out everywhere else" for your own account, "Sign out
  // everywhere" for an admin acting on someone else's). The current device is
  // marked and has no per-row button — the sidebar's Sign out is the way to
  // end this browser's own session, which also runs the cross-tab logout.
  import { onMount } from 'svelte';
  import type { SignedInSession } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import { deviceName, relativeTime, sortSessions } from '$lib/signedInDevices';

  interface Props {
    load: () => Promise<SignedInSession[]>;
    revoke: (id: string) => Promise<void>;
    revokeAll: () => Promise<void>;
    revokeAllLabel: string;
    revokeAllConfirm: string;
    /** The bulk action spares the current device (your own account), so it
     *  only shows when there are others to sign out. */
    keepCurrent?: boolean;
    /** Shown when the list is empty. */
    emptyText?: string;
  }

  let {
    load, revoke, revokeAll, revokeAllLabel, revokeAllConfirm,
    keepCurrent = false, emptyText = 'No signed-in devices.',
  }: Props = $props();

  let sessions = $state<SignedInSession[]>([]);
  let loading = $state(true);
  let error = $state('');
  let busy = $state<Set<string>>(new Set());
  let bulkBusy = $state(false);

  const others = $derived(sessions.filter((s) => !s.current).length);

  async function refresh() {
    error = '';
    try {
      sessions = sortSessions((await load()) ?? []);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not load signed-in devices.';
    } finally {
      loading = false;
    }
  }

  onMount(refresh);

  async function signOut(s: SignedInSession) {
    if (!(await confirmAction(`Sign out ${deviceName(s)}? It will have to sign in again.`))) return;
    busy = new Set(busy).add(s.id);
    try {
      await revoke(s.id);
      sessions = sessions.filter((x) => x.id !== s.id);
      toast.success(`${deviceName(s)} signed out`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Sign-out failed');
    } finally {
      const next = new Set(busy);
      next.delete(s.id);
      busy = next;
    }
  }

  async function signOutAll() {
    if (!(await confirmAction(revokeAllConfirm))) return;
    bulkBusy = true;
    try {
      await revokeAll();
      toast.success('Signed out');
      await refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Sign-out failed');
    } finally {
      bulkBusy = false;
    }
  }
</script>

{#if loading}
  <div class="skeleton"></div>
{:else if error}
  <div class="banner">{error}</div>
{:else}
  {#if sessions.length === 0}
    <p class="muted">{emptyText}</p>
  {:else}
    <ul class="devices">
      {#each sessions as s (s.id)}
        <li class="device">
          <div class="info">
            <div class="name">
              {deviceName(s)}
              {#if s.current}<span class="this">This device</span>{/if}
            </div>
            <div class="meta">
              {#if s.ip_address}<span>{s.ip_address}</span>{/if}
              <span title={new Date(s.last_seen_at).toLocaleString()}>Last used {relativeTime(s.last_seen_at)}</span>
              <span title={new Date(s.created_at).toLocaleString()}>Signed in {relativeTime(s.created_at)}</span>
            </div>
          </div>
          {#if !s.current}
            <button class="btn-signout" disabled={busy.has(s.id) || bulkBusy} onclick={() => signOut(s)}>
              {busy.has(s.id) ? 'Signing out…' : 'Sign out'}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if keepCurrent ? others > 0 : sessions.length > 0}
    <button class="btn-danger" disabled={bulkBusy} onclick={signOutAll}>
      {bulkBusy ? 'Signing out…' : revokeAllLabel}
    </button>
  {/if}
  <p class="hint">"Last used" updates when a device renews its sign-in, about hourly while it's in use.</p>
{/if}

<style>
  .devices { list-style: none; margin: 0 0 0.9rem; padding: 0; display: flex; flex-direction: column; gap: 0.45rem; }
  .device {
    display: flex; align-items: center; justify-content: space-between; gap: 0.75rem;
    padding: 0.6rem 0.75rem; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-hover);
  }
  .info { min-width: 0; }
  .name { font-size: 0.86rem; font-weight: 600; color: var(--text-primary); display: flex; align-items: center; gap: 0.45rem; flex-wrap: wrap; }
  .this { font-size: 0.66rem; font-weight: 700; padding: 0.12rem 0.45rem; border-radius: 999px; background: var(--success-bg); color: var(--success); text-transform: uppercase; letter-spacing: 0.04em; }
  .meta { font-size: 0.74rem; color: var(--text-muted); display: flex; flex-wrap: wrap; gap: 0.2rem 0.8rem; margin-top: 0.15rem; }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .hint { color: var(--text-muted); font-size: 0.74rem; margin: 0.6rem 0 0; }
  .banner { border-radius: 7px; padding: 0.6rem 0.85rem; font-size: 0.82rem; background: var(--error-bg); color: var(--error); }
  .skeleton {
    height: 90px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
  .btn-signout {
    flex-shrink: 0; padding: 0.35rem 0.75rem; background: transparent; color: var(--text-secondary);
    border: 1px solid var(--border-strong); border-radius: 7px; font-size: 0.76rem; font-weight: 500; cursor: pointer;
    transition: background .15s, border-color .15s, color .15s;
  }
  .btn-signout:hover { background: var(--bg-elevated); border-color: var(--text-muted); }
  .btn-signout:disabled, .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-danger {
    padding: 0.45rem 0.9rem; background: var(--error-bg); color: var(--error); border: 1px solid var(--error);
    border-radius: 7px; font-size: 0.8rem; font-weight: 600; cursor: pointer;
  }
</style>
