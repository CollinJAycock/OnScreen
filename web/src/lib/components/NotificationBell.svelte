<script lang="ts">
  import { unreadCount } from '$lib/stores/notifications';
  import { createEventDispatcher } from 'svelte';

  const dispatch = createEventDispatcher();

  export let open = false;

  function toggle() {
    open = !open;
    dispatch('toggle', open);
  }
</script>

<button class="bell" class:active={open} aria-label="Notifications" on:click={toggle}>
  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
    <path fill-rule="evenodd" d="M10 2a6 6 0 00-6 6c0 1.887-.454 3.665-1.257 5.234a.75.75 0 00.515 1.076 32.91 32.91 0 003.256.508 3.5 3.5 0 006.972 0 32.903 32.903 0 003.256-.508.75.75 0 00.515-1.076A11.448 11.448 0 0116 8a6 6 0 00-6-6zM8.05 14.943a33.54 33.54 0 003.9 0 2 2 0 01-3.9 0z" clip-rule="evenodd"/>
  </svg>
  {#if $unreadCount > 0}
    <span class="badge">{$unreadCount > 9 ? '9+' : $unreadCount}</span>
  {/if}
</button>

<style>
  .bell {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    background: none;
    border: none;
    border-radius: 7px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .bell:hover, .bell.active {
    background: var(--bg-hover);
    color: var(--text-secondary);
  }
  .badge {
    position: absolute;
    top: 2px;
    right: 1px;
    min-width: 14px;
    height: 14px;
    padding: 0 3px;
    border-radius: 7px;
    background: var(--accent);
    color: #fff;
    font-size: 0.58rem;
    font-weight: 700;
    line-height: 14px;
    text-align: center;
    pointer-events: none;
  }
</style>
