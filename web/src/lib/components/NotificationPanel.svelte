<script lang="ts">
  import { goto } from '$app/navigation';
  import { notifications, markRead, markAllRead } from '$lib/stores/notifications';
  import type { Notification } from '$lib/api';
  import { createEventDispatcher } from 'svelte';

  const dispatch = createEventDispatcher();

  function timeAgo(ts: number): string {
    const diff = Date.now() - ts;
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs}h ago`;
    const days = Math.floor(hrs / 24);
    return `${days}d ago`;
  }

  function iconFor(type: string): string {
    if (type === 'new_content') return 'M10 18a8 8 0 100-16 8 8 0 000 16zm.75-11.25a.75.75 0 00-1.5 0v2.5h-2.5a.75.75 0 000 1.5h2.5v2.5a.75.75 0 001.5 0v-2.5h2.5a.75.75 0 000-1.5h-2.5v-2.5z';
    if (type === 'scan_complete') return 'M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z';
    return 'M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z';
  }

  async function handleClick(notif: Notification) {
    if (!notif.read) {
      await markRead(notif.id);
    }
    if (notif.item_id) {
      goto(`/watch/${notif.item_id}`);
      dispatch('close');
    }
  }

  async function handleMarkAll() {
    await markAllRead();
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="panel" on:click|stopPropagation>
  <div class="header">
    <span class="title">Notifications</span>
    {#if $notifications.some(n => !n.read)}
      <button class="mark-all" on:click={handleMarkAll}>Mark all read</button>
    {/if}
  </div>

  <div class="list">
    {#if $notifications.length === 0}
      <div class="empty">No notifications</div>
    {:else}
      {#each $notifications as notif (notif.id)}
        <button
          class="item"
          class:unread={!notif.read}
          on:click={() => handleClick(notif)}
        >
          <div class="icon" class:new_content={notif.type === 'new_content'} class:scan_complete={notif.type === 'scan_complete'}>
            <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
              <path fill-rule="evenodd" d={iconFor(notif.type)} clip-rule="evenodd"/>
            </svg>
          </div>
          <div class="content">
            <span class="item-title">{notif.title}</span>
            {#if notif.body}
              <span class="body">{notif.body}</span>
            {/if}
            <span class="time">{timeAgo(notif.created_at)}</span>
          </div>
          {#if !notif.read}
            <div class="dot"></div>
          {/if}
        </button>
      {/each}
    {/if}
  </div>
</div>

<style>
  .panel {
    position: absolute;
    bottom: calc(100% + 8px);
    left: 0;
    width: 300px;
    max-height: 400px;
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    box-shadow: 0 12px 40px var(--shadow);
    overflow: hidden;
    animation: slideUp 0.12s ease-out;
    z-index: 1001;
  }
  @keyframes slideUp {
    from { opacity: 0; transform: translateY(6px); }
    to { opacity: 1; transform: none; }
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.7rem 0.85rem;
    border-bottom: 1px solid var(--border);
  }
  .title {
    font-size: 0.78rem;
    font-weight: 600;
    color: var(--text-primary);
  }
  .mark-all {
    background: none;
    border: none;
    color: var(--accent);
    font-size: 0.68rem;
    font-weight: 500;
    cursor: pointer;
    padding: 0.15rem 0.3rem;
    border-radius: 4px;
    transition: background 0.12s;
  }
  .mark-all:hover {
    background: var(--accent-bg);
  }

  .list {
    overflow-y: auto;
    max-height: 340px;
  }

  .empty {
    padding: 2rem 1rem;
    text-align: center;
    color: var(--text-muted);
    font-size: 0.78rem;
  }

  .item {
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    width: 100%;
    padding: 0.65rem 0.85rem;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    color: var(--text-primary);
    text-align: left;
    cursor: pointer;
    transition: background 0.12s;
  }
  .item:last-child { border-bottom: none; }
  .item:hover { background: var(--bg-hover); }
  .item.unread { background: var(--accent-bg); }
  .item.unread:hover { background: rgba(124,106,247,0.16); }

  .icon {
    width: 28px;
    height: 28px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    background: var(--bg-hover);
    color: var(--text-muted);
    margin-top: 1px;
  }
  .icon.new_content { background: var(--accent-bg); color: var(--accent); }
  .icon.scan_complete { background: var(--success-bg); color: var(--success); }

  .content {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .item-title {
    font-size: 0.76rem;
    font-weight: 500;
    line-height: 1.3;
  }
  .body {
    font-size: 0.7rem;
    color: var(--text-secondary);
    line-height: 1.3;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .time {
    font-size: 0.64rem;
    color: var(--text-muted);
    margin-top: 0.1rem;
  }

  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent);
    flex-shrink: 0;
    margin-top: 6px;
  }

  @media (max-width: 768px) {
    .panel {
      position: fixed;
      bottom: 64px;
      left: 0.5rem;
      right: 0.5rem;
      width: auto;
    }
  }
</style>
