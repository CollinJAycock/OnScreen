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

<!-- data-focus-row: Left / Right stay in the row (the focus manager, as
     for the top nav). Right on a row's last card used to land on a card
     rows above it, the card past the end of a longer row lying further
     right; at a row's end they now do nothing. Up / Down still move
     between rows. -->
<section class="row" data-row={rowKey}>
  <h2>{title}{#if hint}<span class="hint">{hint}</span>{/if}</h2>
  <div class="scroller" data-focus-row>
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

  /* scroll-padding on both sides: a card focused at either end of the
     visible row scrolls in with --page-pad to spare, room for its scale and
     ring, where it used to sit flush with the right edge. */
  .scroller {
    display: flex;
    padding: 16px 0 32px var(--page-pad);
    overflow-x: auto;
    scroll-padding: 0 var(--page-pad);
  }

  /* The trailing inset as a spacer, not padding-right: older Chromium
     (webOS 6 runs 79) doesn't reliably count a scroll box's inline-end
     padding as scrollable (the spec only settled on it later), and without
     it the last card could scroll only to the very edge, its ring cut
     there. A flex item scrolls the same in every engine. */
  .scroller::after {
    content: '';
    flex: 0 0 var(--page-pad);
  }

  /* Cards are spaced with a margin, not flexbox `gap` (Chrome 84): webOS 6
     runs Chromium 79, where the row's posters would touch. :global() keeps
     Svelte 5 from scoping the inner part as :where() (Chrome 88), which
     Chromium 79 would drop the whole rule for; the cards are another
     component's elements anyway. */
  .scroller > :global(* + *) {
    margin-left: var(--card-gap);
  }

  .scroller::-webkit-scrollbar {
    display: none;
  }
</style>
