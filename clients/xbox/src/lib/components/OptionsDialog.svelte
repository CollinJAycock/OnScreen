<script lang="ts">
  // A small D-pad dialog: a title, an optional message, a vertical list of
  // actions and Cancel. Used for card options (Continue Watching, episode
  // cards) and confirmations. use:focusScope keeps the D-pad inside it; Back
  // cancels (pushed on the back stack while open). The caller owns focus
  // after close — typically it re-focuses the card that opened it.
  import { onMount } from 'svelte';
  import { focusable, focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';

  interface Option {
    label: string;
    onselect: () => void;
    danger?: boolean;
  }
  interface Props {
    title: string;
    message?: string;
    options: Option[];
    oncancel: () => void;
    /** Start on Cancel instead of the first option (destructive confirms). */
    focusCancel?: boolean;
    /** The option to start on: a menu's current choice (the checked sort,
     *  genre or scope), so OK alone keeps it and the D-pad starts from it.
     *  Out of range falls back to the first. */
    initialIndex?: number;
  }
  let { title, message, options, oncancel, focusCancel = false, initialIndex = 0 }: Props = $props();

  // Applies as the rows mount (focusable's autofocus), like option 0 did.
  const startAt = $derived(initialIndex >= 0 && initialIndex < options.length ? initialIndex : 0);

  onMount(() =>
    focusManager.pushBack(() => {
      oncancel();
      return true;
    }),
  );
</script>

<div class="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="options-dialog-title" use:focusScope>
  <div class="dialog">
    <div class="dialog-title" id="options-dialog-title">{title}</div>
    {#if message}<div class="dialog-body">{message}</div>{/if}
    <!-- A long list (a library's genres) scrolls inside the dialog: the
         focus manager's scrollIntoView keeps the focused row in view. -->
    <div class="dialog-actions" class:scroll={options.length > 8}>
      {#each options as opt, i (i)}
        <button
          use:focusable={{ autofocus: !focusCancel && i === startAt }}
          class="dialog-btn"
          class:danger={opt.danger}
          onclick={opt.onselect}
        >
          {opt.label}
        </button>
      {/each}
      <button use:focusable={{ autofocus: focusCancel || options.length === 0 }} class="dialog-btn" onclick={oncancel}>
        Cancel
      </button>
    </div>
  </div>
</div>

<style>
  /* Spelled-out offsets and sibling margins instead of `inset` (Chrome 87)
     and flexbox `gap` (Chrome 84): webOS 6 runs Chromium 79. */
  .dialog-backdrop {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    background: rgba(0, 0, 0, 0.72);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .dialog {
    background: var(--bg-secondary, #1f1f24);
    border: 2px solid var(--border-strong);
    border-radius: 16px;
    padding: 36px 40px;
    width: 760px;
    max-width: 90vw;
    box-shadow: 0 12px 48px rgba(0, 0, 0, 0.6);
  }
  .dialog-title {
    font-size: var(--font-lg);
    margin-bottom: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dialog-body {
    font-size: var(--font-md);
    color: var(--text-secondary);
    line-height: 1.45;
    margin-bottom: 8px;
  }
  .dialog-actions {
    display: flex;
    flex-direction: column;
    margin-top: 24px;
  }
  /* :global() on the second part: Svelte 5 would scope it as :where(),
     which Chromium 79 doesn't know, and drop the rule there. */
  .dialog-btn + :global(.dialog-btn) {
    margin-top: 14px;
  }
  /* Room around the rows (padding, cancelled by the negative margin) so the
     focus ring and its 1.06 scale aren't clipped by the scroll box at the
     ends of the list; scroll-padding keeps the same room around a row the
     focus manager scrolls in mid-list (scrollIntoView aligns it with the
     scroll box's edge less the padding), which sat flush with the edge,
     its ring cut. */
  .dialog-actions.scroll {
    max-height: 640px;
    overflow-y: auto;
    padding: 12px 28px;
    margin: 12px -28px 0;
    scroll-padding: 24px 0;
  }
  .dialog-actions.scroll::-webkit-scrollbar {
    display: none;
  }
  .dialog-actions.scroll > :global(.dialog-btn) {
    flex-shrink: 0;
  }
  .dialog-btn {
    font-family: inherit;
    font-size: var(--font-md);
    text-align: left;
    padding: 18px 28px;
    border-radius: 12px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }
  .dialog-btn.danger {
    color: #fca5a5;
  }
</style>
