<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';

  // In place of the splash, not on top of it: no history entry leads back
  // to a screen that only redirects (Back itself is the app's, lib/appExit).
  onMount(() => {
    if (!api.getOrigin()) {
      goto('#/setup', { replaceState: true });
    } else if (!api.getToken()) {
      goto('#/login', { replaceState: true });
    } else {
      goto('#/hub', { replaceState: true });
    }
  });
</script>

<div class="splash">
  <h1>OnScreen</h1>
</div>

<style>
  .splash {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
  }

  h1 {
    font-size: var(--font-2xl);
    color: var(--accent);
    margin: 0;
  }
</style>
