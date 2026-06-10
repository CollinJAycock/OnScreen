<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  import { afterNavigate } from '$app/navigation';
  import { focusManager } from '$lib/focus/manager';
  import { registerTizenKeys } from '$lib/focus/keys';
  import { avplay } from '$lib/player/avplay';

  let { children } = $props();

  // Re-seat focus after every route change: if the previously-focused
  // node unmounted (or the new page has no autofocus target), focus
  // the first focusable so the visible ring never silently disappears.
  // No-op when an autofocus already landed — refocus() bails if
  // `current` is still attached.
  afterNavigate(() => focusManager.refocus());

  // True when AVPlay was torn down by a background event so foreground
  // restore() only fires if we actually suspended (not for unrelated
  // visibility flaps with no active playback).
  let suspendedForBackground = false;

  // App pause/resume → release / re-prepare the hardware decoder.
  // Tizen fires `visibilitychange` (document.hidden) when the app is
  // backgrounded (Home/launcher, input switch, screen off). AVPlay's
  // decoder stays bound across a background unless we release it, so
  // resume comes back black or stale. Suspend on hide, restore on show.
  function onVisibilityChange() {
    if (document.hidden) {
      suspendedForBackground = avplay.suspend();
    } else if (suspendedForBackground) {
      suspendedForBackground = false;
      avplay.restore();
    }
  }

  onMount(() => {
    // Tell the Tizen firmware to forward Back, MediaPlay/Pause,
    // and the colored A/B/C/D buttons into our keydown handler.
    // Without this only the always-on D-pad + Enter come through.
    // No-op outside the Tizen webview (e.g., `vite dev`).
    registerTizenKeys();
    focusManager.init();
    document.addEventListener('visibilitychange', onVisibilityChange);
    return () => {
      document.removeEventListener('visibilitychange', onVisibilityChange);
      focusManager.destroy();
    };
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
