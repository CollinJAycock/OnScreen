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
  }
  let { title, message, options, oncancel, focusCancel = false }: Props = $props();

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
    <div class="dialog-actions">
      {#each options as opt, i (opt.label)}
        <button
          use:focusable={{ autofocus: !focusCancel && i === 0 }}
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
  .dialog-backdrop {
    position: fixed;
    inset: 0;
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
    gap: 14px;
    margin-top: 24px;
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
