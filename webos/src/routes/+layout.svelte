<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  import { focusManager } from '$lib/focus/manager';

  let { children } = $props();

  onMount(() => {
    focusManager.init();
    return () => focusManager.destroy();
  });
</script>

<main class="tv-root">
  {@render children()}
</main>

<style>
  .tv-root {
    width: 1920px;
    height: 1080px;
    position: relative;
    overflow: hidden;
    background: var(--bg-primary);
    color: var(--text-primary);
  }

  @media (max-width: 1919px) {
    .tv-root {
      width: 100vw;
      height: 100vh;
      transform-origin: top left;
    }
  }
</style>
