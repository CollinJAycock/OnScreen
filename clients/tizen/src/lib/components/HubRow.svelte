<script lang="ts">
  interface Props {
    title: string;
    /** Small muted note next to the title (e.g. "Hold OK for options"). */
    hint?: string;
    /** Stable key on the section (data-row) so a page can find its cards. */
    rowKey?: string;
    children: import('svelte').Snippet;
  }
  let { title, hint, rowKey, children }: Props = $props();
</script>

<section class="row" data-row={rowKey}>
  <h2>{title}{#if hint}<span class="hint">{hint}</span>{/if}</h2>
  <div class="scroller">
    {@render children()}
  </div>
</section>

<style>
  .row {
    margin-bottom: var(--row-gap);
  }

  h2 {
    font-size: var(--font-lg);
    margin: 0 0 20px;
    padding: 0 var(--page-pad);
  }

  .hint {
    margin-left: 24px;
    font-size: var(--font-xs);
    font-weight: 400;
    color: var(--text-muted);
  }

  .scroller {
    display: flex;
    gap: var(--card-gap);
    padding: 16px var(--page-pad) 32px;
    overflow-x: auto;
    scroll-padding-left: var(--page-pad);
  }

  .scroller::-webkit-scrollbar {
    display: none;
  }
</style>
