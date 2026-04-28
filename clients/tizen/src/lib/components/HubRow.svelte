<script lang="ts">
  import { focusScope } from '$lib/focus/focusable';

  interface Props {
    title: string;
    children: import('svelte').Snippet;
  }
  let { title, children }: Props = $props();
</script>

<section class="row" use:focusScope>
  <h2>{title}</h2>
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
