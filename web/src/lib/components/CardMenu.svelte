<script lang="ts">
  // Small "⋯" action menu for poster cards. Keyboard: Enter/Space opens
  // and focuses the first item, arrows move, Escape closes and returns
  // focus to the trigger, Tab closes. Every click is preventDefault'ed
  // and stopped so the menu is safe inside (or next to) a card link.
  import { tick, onDestroy } from 'svelte';

  type CardMenuAction = { label: string; onSelect: () => void };

  /** Accessible name of the trigger, e.g. "More actions for Alien". */
  export let label: string;
  export let actions: CardMenuAction[] = [];

  let open = false;
  let root: HTMLDivElement;
  let trigger: HTMLButtonElement;
  let menuEl: HTMLDivElement;

  function items(): HTMLButtonElement[] {
    return menuEl ? Array.from(menuEl.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')) : [];
  }

  async function openMenu() {
    open = true;
    await tick();
    items()[0]?.focus();
    window.addEventListener('click', onWindowClick, true);
  }

  function closeMenu(refocus = false) {
    if (!open) return;
    open = false;
    window.removeEventListener('click', onWindowClick, true);
    if (refocus) trigger?.focus();
  }

  function onWindowClick(e: MouseEvent) {
    if (root && !root.contains(e.target as Node)) closeMenu();
  }

  function onTriggerClick(e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    if (open) closeMenu();
    else openMenu();
  }

  function onMenuKeydown(e: KeyboardEvent) {
    const list = items();
    const i = list.indexOf(document.activeElement as HTMLButtonElement);
    switch (e.key) {
      case 'Escape':
        e.preventDefault();
        e.stopPropagation();
        closeMenu(true);
        break;
      case 'ArrowDown':
        e.preventDefault();
        list[(i + 1) % list.length]?.focus();
        break;
      case 'ArrowUp':
        e.preventDefault();
        list[(i - 1 + list.length) % list.length]?.focus();
        break;
      case 'Home':
        e.preventDefault();
        list[0]?.focus();
        break;
      case 'End':
        e.preventDefault();
        list[list.length - 1]?.focus();
        break;
      case 'Tab':
        closeMenu();
        break;
    }
  }

  function select(e: MouseEvent, action: CardMenuAction) {
    e.preventDefault();
    e.stopPropagation();
    closeMenu(true);
    action.onSelect();
  }

  onDestroy(() => {
    if (typeof window !== 'undefined') window.removeEventListener('click', onWindowClick, true);
  });
</script>

<div class="card-menu" class:open bind:this={root}>
  <button
    type="button"
    class="card-menu-trigger"
    bind:this={trigger}
    aria-label={label}
    title={label}
    aria-haspopup="menu"
    aria-expanded={open}
    on:click={onTriggerClick}
  >
    <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14" aria-hidden="true">
      <circle cx="3" cy="8" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="13" cy="8" r="1.5"/>
    </svg>
  </button>
  {#if open}
    <div class="card-menu-list" role="menu" aria-label={label} tabindex="-1" bind:this={menuEl} on:keydown={onMenuKeydown}>
      {#each actions as action (action.label)}
        <button type="button" role="menuitem" class="card-menu-item" on:click={(e) => select(e, action)}>
          {action.label}
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .card-menu { position: relative; display: inline-flex; }
  .card-menu-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .card-menu-trigger:hover,
  .card-menu.open .card-menu-trigger { background: var(--bg-hover); color: var(--text-primary); }
  .card-menu-trigger:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .card-menu-list {
    position: absolute;
    right: 0;
    bottom: calc(100% + 4px);
    z-index: 20;
    min-width: 150px;
    display: flex;
    flex-direction: column;
    padding: 0.25rem;
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    box-shadow: 0 12px 28px var(--shadow);
  }
  .card-menu-item {
    text-align: left;
    padding: 0.45rem 0.6rem;
    border: none;
    border-radius: 5px;
    background: transparent;
    color: var(--text-secondary);
    font-size: 0.78rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .card-menu-item:hover,
  .card-menu-item:focus-visible { background: var(--bg-hover); color: var(--text-primary); outline: none; }
</style>
