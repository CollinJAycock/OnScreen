<script lang="ts">
  // The player moves from item to item on its own route: Up Next, the next
  // track of an album, the next chapter of a book (goToNext →
  // #/watch/<next id>). SvelteKit reuses the page component across a
  // params-only navigation, so the player's onMount (the item load, the
  // session start) wouldn't run again and its cleanup (heartbeat, session
  // stop) wouldn't run for the item it left. Keying on the id gives every
  // item a fresh player. playerEpoch does the same for the item already
  // open: "play on this TV" of the item playing here, at another position,
  // is the same URL (see playItem in nav.ts).
  import { page } from '$app/state';
  import { playerEpoch } from '$lib/nav';

  let { children } = $props();
</script>

{#key `${page.params.id}:${$playerEpoch}`}
  {@render children()}
{/key}
