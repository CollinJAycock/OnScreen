<script lang="ts">
  import { focusable } from '$lib/focus/focusable';

  interface Props {
    onclick?: () => void;
    autofocus?: boolean;
    variant?: 'default' | 'primary';
    children?: import('svelte').Snippet;
  }
  let { onclick, autofocus, variant = 'default', children }: Props = $props();
</script>

<button
  use:focusable={{ autofocus }}
  class="btn {variant}"
  {onclick}
>
  {@render children?.()}
</button>

<style>
  .btn {
    font-size: var(--font-md);
    font-family: inherit;
    padding: 18px 36px;
    border-radius: 12px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }

  .btn.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }
</style>
