<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  import { focusManager } from '$lib/focus/manager';
  import { registerTizenKeys } from '$lib/focus/keys';

  let { children } = $props();

  onMount(() => {
    // Tell the Tizen firmware to forward Back, MediaPlay/Pause,
    // and the colored A/B/C/D buttons into our keydown handler.
    // Without this only the always-on D-pad + Enter come through.
    // No-op outside the Tizen webview (e.g., `vite dev`).
    registerTizenKeys();
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
