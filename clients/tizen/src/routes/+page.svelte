<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';

  onMount(() => {
    if (!api.getOrigin()) {
      goto('/setup');
    } else if (!api.getToken()) {
      goto('/login');
    } else {
      goto('/hub');
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
