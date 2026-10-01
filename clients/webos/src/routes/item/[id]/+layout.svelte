<script lang="ts">
  // Item pages open other item pages on the same route: artist → album,
  // author → series, a show's season. SvelteKit reuses the page component
  // across a params-only navigation, so its onMount (the load, the Back
  // handler, the autofocus) wouldn't run again and the previous item would
  // stay on screen. Keying on the id gives every item a fresh page.
  import { page } from '$app/state';

  let { children } = $props();
</script>

{#key page.params.id}
  {@render children()}
{/key}
