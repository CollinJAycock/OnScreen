<script lang="ts">
  import { toast } from '$lib/stores/toast';
  import { fly, fade } from 'svelte/transition';
</script>

{#if $toast.length > 0}
  <div class="toast-container" aria-live="polite">
    {#each $toast as t (t.id)}
      <div
        class="toast toast-{t.type}"
        in:fly={{ x: 80, duration: 250 }}
        out:fade={{ duration: 150 }}
      >
        <div class="toast-icon">
          {#if t.type === 'success'}
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.857-9.809a.75.75 0 00-1.214-.882l-3.483 4.79-1.88-1.88a.75.75 0 10-1.06 1.061l2.5 2.5a.75.75 0 001.137-.089l4-5.5z" clip-rule="evenodd"/>
            </svg>
          {:else if t.type === 'error'}
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clip-rule="evenodd"/>
            </svg>
          {:else}
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z" clip-rule="evenodd"/>
            </svg>
          {/if}
        </div>
        <span class="toast-message">{t.message}</span>
        <button class="toast-close" on:click={() => toast.removeToast(t.id)} aria-label="Dismiss">
          <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
            <path d="M6.28 5.22a.75.75 0 00-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 101.06 1.06L10 11.06l3.72 3.72a.75.75 0 101.06-1.06L11.06 10l3.72-3.72a.75.75 0 00-1.06-1.06L10 8.94 6.28 5.22z"/>
          </svg>
        </button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .toast-container {
    position: fixed;
    bottom: 1.25rem;
    right: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    z-index: 9999;
    max-width: 380px;
    width: 100%;
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    padding: 0.7rem 0.85rem;
    border-radius: 8px;
    font-size: 0.8rem;
    line-height: 1.45;
    box-shadow: 0 8px 24px var(--shadow);
    pointer-events: auto;
  }

  .toast-success {
    background: var(--success-bg);
    border: 1px solid var(--success);
    color: var(--success);
  }

  .toast-error {
    background: var(--error-bg);
    border: 1px solid var(--error);
    color: var(--error);
  }

  .toast-info {
    background: var(--info-bg);
    border: 1px solid var(--info);
    color: var(--info);
  }

  .toast-icon {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    margin-top: 0.05rem;
  }

  .toast-message {
    flex: 1;
    min-width: 0;
    word-break: break-word;
  }

  .toast-close {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    background: none;
    border: none;
    border-radius: 5px;
    color: inherit;
    opacity: 0.5;
    cursor: pointer;
    transition: opacity 0.12s, background 0.12s;
    margin-top: -0.05rem;
  }
  .toast-close:hover {
    opacity: 1;
    background: var(--bg-hover);
  }
</style>
